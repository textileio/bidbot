package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/joho/godotenv"
	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	mbase "github.com/multiformats/go-multibase"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/lib/common"
	"github.com/textileio/bidbot/lib/dshelper"
	"github.com/textileio/bidbot/lib/filclient"
	"github.com/textileio/bidbot/lib/finalizer"
	"github.com/textileio/bidbot/lib/marketpeer"
	golog "github.com/textileio/go-log/v2"

	"github.com/textileio/bidbot/httpapi"
	"github.com/textileio/bidbot/service"
	"github.com/textileio/bidbot/service/limiter"
	"github.com/textileio/bidbot/service/lotusclient"
	"github.com/textileio/bidbot/service/store"
)

var (
	cliName           = "bidbot"
	defaultConfigPath = filepath.Join(os.Getenv("HOME"), "."+cliName)
	log               = golog.Logger(cliName)
	v                 = viper.New()

	dealsListFields = []string{"ID", "DealSize", "DealDuration", "Status", "AskPrice", "VerifiedAskPrice",
		"StartEpoch", "DataURIFetchAttempts", "CreatedAt", "ErrorCause"}
)

func init() {
	_ = godotenv.Load(".env")
	configPath := os.Getenv("BIDBOT_PATH")
	if configPath == "" {
		configPath = defaultConfigPath
	}
	_ = godotenv.Load(filepath.Join(configPath, ".env"))

	rootCmd.AddCommand(initCmd, daemonCmd, idCmd, dealsCmd, downloadCmd)
	dealsCmd.AddCommand(dealsListCmd)
	dealsCmd.AddCommand(dealsShowCmd)

	commonFlags := []common.Flag{
		{
			Name:        "http-port",
			DefValue:    "9999",
			Description: "HTTP API listen address",
		},
	}
	daemonFlags := []common.Flag{
		{
			Name:        "miner-addr",
			DefValue:    "",
			Description: "Miner address (fxxxx); required",
		},
		{
			Name:        "wallet-addr-sig",
			DefValue:    "",
			Description: "Miner wallet address signature; required; see 'bidbot help init' for instructions",
		},
		{
			Name:        "ask-price",
			DefValue:    0,
			Description: "Bid ask price for deals in attoFIL per GiB per epoch; default is 100 nanoFIL",
		},
		{
			Name:        "verified-ask-price",
			DefValue:    0,
			Description: "Bid ask price for verified deals in attoFIL per GiB per epoch; default is 100 nanoFIL",
		},
		{
			Name:        "fast-retrieval",
			DefValue:    true,
			Description: "Offer deals with fast retrieval",
		},
		{
			Name:        "deal-start-window",
			DefValue:    60 * 24 * 2,
			Description: "Number of epochs after which won deals must start on-chain; default is ~one day",
		},
		{
			Name:        "deal-duration-min",
			DefValue:    auction.MinDealDuration,
			Description: "Minimum deal duration to bid on in epochs; default is ~6 months",
		},
		{
			Name:        "deal-duration-max",
			DefValue:    auction.MaxDealDuration,
			Description: "Maximum deal duration to bid on in epochs; default is ~1 year",
		},
		{
			Name:        "deal-size-min",
			DefValue:    56 * 1024,
			Description: "Minimum deal size to bid on in bytes",
		},
		{
			Name:        "deal-size-max",
			DefValue:    32 * 1024 * 1024 * 1024,
			Description: "Maximum deal size to bid on in bytes",
		},
		{
			Name:        "discard-orphan-deals-after",
			DefValue:    24 * time.Hour,
			Description: "The timeout before discarding deal with no progress",
		},
		{
			Name:     "running-bytes-limit",
			DefValue: "",
			Description: `Maximum running total bytes in the deals to bid for a period of time.
In the form of '10MiB/1m', '500 tb/24h' or '5 PiB / 128h', etc.
See https://en.wikipedia.org/wiki/Byte#Multiple-byte_units for valid byte units.
Default to no limit. Be aware that the bytes counter resets when bidbot restarts.
Also take the file system overhead into consideration when calculating the limit.`,
		},
		{
			Name:        "deal-data-fetch-attempts",
			DefValue:    3,
			Description: "Number of times fetching deal data will be attempted before failing",
		},
		{Name: "lotus-miner-api-maddr", DefValue: "/ip4/127.0.0.1/tcp/2345/http",
			Description: "Lotus miner API multiaddress"},
		{Name: "lotus-miner-api-token", DefValue: "", Description: "Lotus miner API authorization token"},
		{Name: "lotus-api-conn-retries", DefValue: "2", Description: "Lotus API connection retries"},
		{Name: "lotus-gateway-url", DefValue: "https://api.node.glif.io", Description: "Lotus gateway URL"},
		{Name: "metrics-addr", DefValue: ":9090", Description: "Prometheus listen address"},
		{Name: "log-debug", DefValue: false, Description: "Enable debug level log"},
		{Name: "log-json", DefValue: false, Description: "Enable structured logging"},
	}
	daemonFlags = append(daemonFlags, marketpeer.Flags...)
	dealsFlags := []common.Flag{{Name: "json", DefValue: false,
		Description: "output in json format instead of tabular print"}}
	dealsListFlags := []common.Flag{{Name: "status", DefValue: "",
		Description: "filter by auction statuses, separated by comma"}}

	cobra.OnInitialize(func() {
		v.SetConfigType("json")
		v.SetConfigName("config")
		v.AddConfigPath(os.Getenv("BIDBOT_PATH"))
		v.AddConfigPath(defaultConfigPath)
		_ = v.ReadInConfig()
	})

	common.ConfigureCLI(v, "BIDBOT", commonFlags, rootCmd.PersistentFlags())
	common.ConfigureCLI(v, "BIDBOT", marketpeer.Flags, initCmd.PersistentFlags())
	common.ConfigureCLI(v, "BIDBOT", daemonFlags, daemonCmd.PersistentFlags())
	common.ConfigureCLI(v, "BIDBOT", dealsFlags, dealsCmd.PersistentFlags())
	common.ConfigureCLI(v, "BIDBOT", dealsListFlags, dealsListCmd.PersistentFlags())
}

var rootCmd = &cobra.Command{
	Use:   cliName,
	Short: "Bidbot listens for Filecoin storage deal auction from deal brokers",
	Long: `Bidbot listens for Filecoin storage deal auction from deal brokers.

bidbot will automatically bid on storage deals that pass configured filters at
the configured prices.

To get started, run 'bidbot init' and follow the instructions. 
`,
	Args: cobra.ExactArgs(0),
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initializes bidbot configuration files",
	Long: `Initializes bidbot configuration files and generates a new keypair.

bidbot uses a repository in the local file system. By default, the repo is
located at ~/.bidbot. To change the repo location, set the $BIDBOT_PATH
environment variable:

    export BIDBOT_PATH=/path/to/bidbotrepo

bidbot will fetch and write deal data to a local directory. This directory should be
accessible to Lotus. By default, the directory is located at ~/.bidbot/deal_data.
The change the deal data directory, set the $BIDBOT_DEAL_DATA_DIRECTORY environment variable:

    export BIDBOT_DEAL_DATA_DIRECTORY=/path/to/lotus/accessible/directory
`,
	Args: cobra.ExactArgs(0),
	Run: func(c *cobra.Command, args []string) {
		path, err := marketpeer.WriteConfig(v, "BIDBOT_PATH", defaultConfigPath)
		common.CheckErrf("writing config: %v", err)
		fmt.Printf("Initialized configuration file: %s\n\n", path)

		_, key, err := mbase.Decode(v.GetString("private-key"))
		common.CheckErrf("decoding private key: %v", err)
		priv, err := crypto.UnmarshalPrivateKey(key)
		common.CheckErrf("unmarshaling private key: %v", err)
		id, err := peer.IDFromPrivateKey(priv)
		common.CheckErrf("getting peer id: %v", err)

		signingToken := hex.EncodeToString([]byte(id))

		fmt.Printf(`Bidbot needs a signature from a miner wallet address to authenticate bids.

1. Sign this token with an address from your miner owner Lotus wallet address:

    lotus wallet sign [owner-address] %s

2. Start listening for deal auction using the wallet address and signature from step 1:

    bidbot daemon --miner-addr [address] 
                  --wallet-addr-sig [signature] 
		  --lotus-miner-api-maddr [lotus-miner-api-maddr] 
		  --lotus-miner-api-token [lotus-miner-api-token]

Note: In the event you win an auction, you must use this wallet address to make the deal(s).

Good luck!
`, signingToken)
	},
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run a network-connected bidding bot",
	Long:  "Run a network-connected bidding bot that listens for and bids on storage deal auction.",
	Args:  cobra.ExactArgs(0),
	PersistentPreRun: func(c *cobra.Command, args []string) {
		common.ExpandEnvVars(v, v.AllSettings())
		err := common.ConfigureLogging(v, []string{
			cliName,
			"bidbot/service",
			"bidbot/store",
			"bidbot/datauri",
			"bidbot/api",
			"bidbot/lotus",
			"mpeer",
			"mpeer/pubsub",
			"mpeer/mdns",
		})
		common.CheckErrf("setting log levels: %v", err)
	},
	Run: func(c *cobra.Command, args []string) {
		if v.GetString("miner-addr") == "" {
			common.CheckErr(errors.New("--miner-addr is required. See 'bidbot help init' for instructions"))
		}
		if v.GetString("wallet-addr-sig") == "" {
			common.CheckErr(errors.New("--wallet-addr-sig is required. See 'bidbot help init' for instructions"))
		}

		pconfig, err := marketpeer.GetConfig(v, "BIDBOT_PATH", defaultConfigPath, false)
		common.CheckErrf("getting peer config: %v", err)

		settings, err := marketpeer.MarshalConfig(v, !v.GetBool("log-json"))
		common.CheckErrf("marshaling config: %v", err)
		log.Infof("loaded config: %s", string(settings))

		fin := finalizer.NewFinalizer()
		repoPath := os.Getenv("BIDBOT_PATH")
		if repoPath == "" {
			repoPath = defaultConfigPath
		}
		store, err := dshelper.NewBadgerTxnDatastore(filepath.Join(repoPath, "bidstore"))
		common.CheckErrf("creating datastore: %v", err)
		fin.Add(store)

		err = common.SetupInstrumentation(v.GetString("metrics.addr"))
		common.CheckErrf("booting instrumentation: %v", err)

		walletAddrSig, err := hex.DecodeString(v.GetString("wallet-addr-sig"))
		common.CheckErrf("decoding wallet address signature: %v", err)

		lc, err := lotusclient.New(
			v.GetString("lotus-miner-api-maddr"),
			v.GetString("lotus-miner-api-token"),
			v.GetInt("lotus-api-conn-retries"),
			v.GetBool("fake-mode"),
		)
		common.CheckErrf("creating lotus client: %v", err)
		fin.Add(lc)

		fc, err := filclient.New(v.GetString("lotus-gateway-url"), v.GetBool("fake-mode"))
		common.CheckErrf("creating chain client: %v", err)
		fin.Add(fc)

		dealDataDirectory := os.Getenv("BIDBOT_DEAL_DATA_DIRECTORY")
		if dealDataDirectory == "" {
			dealDataDirectory = filepath.Join(defaultConfigPath, "deal_data")
		}

		var bytesLimiter limiter.Limiter = limiter.NopeLimiter{}

		if limit := v.GetString("running-bytes-limit"); limit != "" {
			lim, err := parseRunningBytesLimit(limit)
			common.CheckErrf(fmt.Sprintf("parsing '%s': %%w", limit), err)
			bytesLimiter = lim
		}

		config := service.Config{
			Peer: pconfig,
			BidParams: service.BidParams{
				MinerAddr:               v.GetString("miner-addr"),
				WalletAddrSig:           walletAddrSig,
				AskPrice:                v.GetInt64("ask-price"),
				VerifiedAskPrice:        v.GetInt64("verified-ask-price"),
				FastRetrieval:           v.GetBool("fast-retrieval"),
				DealStartWindow:         v.GetUint64("deal-start-window"),
				DealDataDirectory:       dealDataDirectory,
				DealDataFetchAttempts:   v.GetUint32("deal-data-fetch-attempts"),
				DiscardOrphanDealsAfter: v.GetDuration("discard-orphan-deals-after"),
			},
			AuctionFilters: service.AuctionFilters{
				DealDuration: service.MinMaxFilter{
					Min: v.GetUint64("deal-duration-min"),
					Max: v.GetUint64("deal-duration-max"),
				},
				DealSize: service.MinMaxFilter{
					Min: v.GetUint64("deal-size-min"),
					Max: v.GetUint64("deal-size-max"),
				},
			},
			BytesLimiter: bytesLimiter,
		}
		serv, err := service.New(config, store, lc, fc)
		common.CheckErrf("starting service: %v", err)
		fin.Add(serv)

		err = serv.Subscribe(true)
		common.CheckErrf("subscribing to deal auction feed: %v", err)

		api, err := httpapi.NewServer(":"+v.GetString("http-port"), serv)
		common.CheckErrf("creating http API server: %v", err)
		fin.Add(api)

		common.HandleInterrupt(func() {
			common.CheckErr(fin.Cleanupf("closing service: %v", nil))
		})
	},
}

var idCmd = &cobra.Command{
	Use:   "id",
	Short: "shows the id, public key and addresses of the bidbot",
	Args:  cobra.ExactArgs(0),
	Run: func(c *cobra.Command, args []string) {
		res, err := http.Get(urlFor("id"))
		common.CheckErr(err)
		defer func() {
			err := res.Body.Close()
			common.CheckErr(err)
		}()
		b, _ := ioutil.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			log.Fatalf("%s: %s", res.Status, string(b))
		}
		fmt.Println(string(b))
	},
}

var dealsCmd = &cobra.Command{
	Use: "deals",
	Aliases: []string{
		"deal",
	},
	Short: "Interact with storage deals",
	Long:  "Interact with storage deals.",
	Args:  cobra.ExactArgs(0),
}

var dealsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List deals, optionally filtered by status",
	Args:  cobra.ExactArgs(0),
	Run: func(c *cobra.Command, args []string) {
		var query string
		if status := v.GetString("status"); status != "" {
			query = fmt.Sprintf("?status=%s", url.QueryEscape(status))
		}
		bids := getBids(urlFor("deals") + query)
		if v.GetBool("json") {
			b, err := json.MarshalIndent(bids, "", "\t")
			common.CheckErr(err)
			fmt.Println(string(b))
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.DiscardEmptyColumns)
		for i, bid := range bids {
			if i == 0 {
				for _, field := range dealsListFields {
					_, err := fmt.Fprintf(w, "%s\t", field)
					common.CheckErr(err)
				}
				_, err := fmt.Fprintln(w, "")
				common.CheckErr(err)
			}
			value := reflect.ValueOf(bid)
			for _, field := range dealsListFields {
				_, err := fmt.Fprintf(w, "%v\t", value.FieldByName(field))
				common.CheckErr(err)
			}
			_, err := fmt.Fprintln(w, "")
			common.CheckErr(err)
		}
		_ = w.Flush()
	},
}

var dealsShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show details of one deal",
	Long:  `Show details of one deal, specified by the bid ID, which can be obtained by 'bidbot deals list'`,
	Args:  cobra.ExactArgs(1),
	Run: func(c *cobra.Command, args []string) {
		bids := getBids(urlFor("deals", args[0]))
		if len(bids) == 0 {
			return
		}
		bid := bids[0]
		if v.GetBool("json") {
			b, err := json.MarshalIndent(bid, "", "\t")
			common.CheckErr(err)
			fmt.Println(string(b))
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
		typ := reflect.TypeOf(bid)
		value := reflect.ValueOf(bid)
		for i := 0; i < typ.NumField(); i++ {
			_, err := fmt.Fprintf(w, "%s:\t%v\n", typ.Field(i).Name, value.Field(i))
			common.CheckErr(err)
		}
		_ = w.Flush()
	},
}

var downloadCmd = &cobra.Command{
	Use:   "download <cid> <uri>",
	Short: "Download and write storage deal data to disk",
	Long: `Downloads and writes storage deal data to disk by cid and uri.

Deal data is written to BIDBOT_DEAL_DATA_DIRECTORY in CAR format.
`,
	Args: cobra.ExactArgs(2),
	Run: func(c *cobra.Command, args []string) {
		base := urlFor("datauri") + "?"
		params := url.Values{}
		params.Add("cid", args[0])
		params.Add("uri", args[1])
		res, err := http.Get(base + params.Encode())
		common.CheckErr(err)
		defer func() {
			err := res.Body.Close()
			common.CheckErr(err)
		}()
		if _, err = io.Copy(os.Stdout, res.Body); err != io.EOF {
			common.CheckErr(err)
		}
	},
}

func main() {
	common.CheckErr(rootCmd.Execute())
}

func urlFor(parts ...string) string {
	u := "http://127.0.0.1:" + v.GetString("http-port")
	if len(parts) > 0 {
		u += "/" + path.Join(parts...)
	}
	return u
}

func getBids(u string) (bids []store.Bid) {
	res, err := http.Get(u)
	common.CheckErr(err)
	defer func() {
		err := res.Body.Close()
		common.CheckErr(err)
	}()
	if res.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(res.Body)
		log.Fatalf("%s: %s", res.Status, string(b))
	}
	decoder := json.NewDecoder(res.Body)
	err = decoder.Decode(&bids)
	common.CheckErr(err)
	return
}

func parseRunningBytesLimit(s string) (limiter.Limiter, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return nil, errors.New("should be separated by forward slash (/)")
	}
	sBytes := strings.TrimSpace(parts[0])
	nBytes, err := humanize.ParseBytes(sBytes)
	if err != nil {
		return nil, err
	}
	ds := strings.TrimSpace(parts[1])
	d, err := time.ParseDuration(ds)
	if err != nil {
		return nil, err
	}
	return limiter.NewRunningTotalLimiter(d, nBytes), nil
}
