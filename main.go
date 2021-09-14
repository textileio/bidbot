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
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/joho/godotenv"
	"github.com/libp2p/go-libp2p-core/crypto"
	core "github.com/libp2p/go-libp2p-core/peer"
	mbase "github.com/multiformats/go-multibase"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/textileio/bidbot/buildinfo"
	"github.com/textileio/bidbot/httpapi"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/lib/dshelper"
	"github.com/textileio/bidbot/lib/filclient"
	"github.com/textileio/bidbot/lib/peerflags"
	"github.com/textileio/bidbot/service"
	"github.com/textileio/bidbot/service/limiter"
	"github.com/textileio/bidbot/service/lotusclient"
	"github.com/textileio/bidbot/service/pricing"
	"github.com/textileio/bidbot/service/store"
	"github.com/textileio/cli"
	"github.com/textileio/go-libp2p-pubsub-rpc/finalizer"
	golog "github.com/textileio/go-log/v2"
)

var (
	cliName           = "bidbot"
	defaultConfigPath = filepath.Join(os.Getenv("HOME"), "."+cliName)
	log               = golog.Logger(cliName)
	v                 = viper.New()

	dealsListFields = []string{"ID", "DealSize", "DealDuration", "Status", "AskPrice", "VerifiedAskPrice",
		"StartEpoch", "DataURIFetchAttempts", "CreatedAt", "ErrorCause"}
	validStorageProviderID = regexp.MustCompile("^[a-z]0[0-9]+$")
)

func init() {
	_ = godotenv.Load(".env")
	configPath := os.Getenv("BIDBOT_PATH")
	if configPath == "" {
		configPath = defaultConfigPath
	}
	_ = godotenv.Load(filepath.Join(configPath, ".env"))

	rootCmd.AddCommand(initCmd, daemonCmd, idCmd, versionCmd, dealsCmd, downloadCmd)
	dealsCmd.AddCommand(dealsListCmd)
	dealsCmd.AddCommand(dealsShowCmd)

	commonFlags := []cli.Flag{
		{
			Name:        "http-port",
			DefValue:    "9999",
			Description: "HTTP API listen address",
		},
	}
	daemonFlags := []cli.Flag{
		{
			Name:        "miner-addr",
			DefValue:    "",
			Description: "Miner address (f0xxxx); depreciated, use storage-provider-id",
		},
		{
			Name:        "storage-provider-id",
			DefValue:    "",
			Description: "Storage Provider ID (f0xxxx); required",
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
			DefValue:    0,
			Description: "Number of epochs after which won deals must start on-chain; required",
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
Also take the file system overhead into consideration when calculating the limit.
No limit by default.`,
		},
		{
			Name:        "concurrent-imports-limit",
			DefValue:    0,
			Description: `If bigger than zero, only run that many imports concurrently. Zero means no limits.`,
		},

		{
			Name:     "sealing-sectors-limit",
			DefValue: 0,
			Description: `If bigger than zero, stop bidding if the number of Lotus sealing sectors exceeds this limit.
Zero means no limits`,
		},
		{
			Name:        "deal-data-fetch-attempts",
			DefValue:    3,
			Description: "Number of times fetching deal data will be attempted before failing",
		},
		{
			Name:        "deal-data-fetch-timeout",
			DefValue:    "3h",
			Description: `The timeout to fetch deal data. Be conservative to leave enough room for network instability.`,
		},

		{
			Name:        "cid-gravity-key",
			DefValue:    "",
			Description: "The API key to the CID gravity system. No CID gravity integration by default.",
		},

		{
			Name:        "cid-gravity-default-reject",
			DefValue:    true,
			Description: "Stop bidding if there's any problem loading cid-gravity pricing.",
		},

		{Name: "lotus-miner-api-maddr", DefValue: "/ip4/127.0.0.1/tcp/2345/http",
			Description: "Lotus miner API multiaddress"},
		{Name: "lotus-miner-api-token", DefValue: "",
			Description: "Lotus miner API authorization token with write permission"},
		{Name: "lotus-api-conn-retries", DefValue: "2", Description: "Lotus API connection retries"},
		{Name: "lotus-gateway-url", DefValue: "https://api.node.glif.io", Description: "Lotus gateway URL"},
		{Name: "log-debug", DefValue: false, Description: "Enable debug level log"},
		{Name: "log-json", DefValue: false, Description: "Enable structured logging"},
	}
	daemonFlags = append(daemonFlags, peerflags.Flags...)
	dealsFlags := []cli.Flag{{Name: "json", DefValue: false,
		Description: "output in json format instead of tabular print"}}
	dealsListFlags := []cli.Flag{{Name: "status", DefValue: "",
		Description: "filter by auction statuses, separated by comma"}}

	cobra.OnInitialize(func() {
		v.SetConfigType("json")
		v.SetConfigName("config")
		v.AddConfigPath(os.Getenv("BIDBOT_PATH"))
		v.AddConfigPath(defaultConfigPath)
		_ = v.ReadInConfig()
	})

	cli.ConfigureCLI(v, "BIDBOT", commonFlags, rootCmd.PersistentFlags())
	cli.ConfigureCLI(v, "BIDBOT", peerflags.Flags, initCmd.PersistentFlags())
	cli.ConfigureCLI(v, "BIDBOT", daemonFlags, daemonCmd.PersistentFlags())
	cli.ConfigureCLI(v, "BIDBOT", dealsFlags, dealsCmd.PersistentFlags())
	cli.ConfigureCLI(v, "BIDBOT", dealsListFlags, dealsListCmd.PersistentFlags())
}

var rootCmd = &cobra.Command{
	Use:   cliName,
	Short: "Bidbot listens for Filecoin storage deal auctions from deal brokers",
	Long: `Bidbot listens for Filecoin storage deal auctions from deal brokers.

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
		path, err := peerflags.WriteConfig(v, "BIDBOT_PATH", defaultConfigPath)
		cli.CheckErrf("writing config: %v", err)
		fmt.Printf("Initialized configuration file: %s\n\n", path)

		_, key, err := mbase.Decode(v.GetString("private-key"))
		cli.CheckErrf("decoding private key: %v", err)
		priv, err := crypto.UnmarshalPrivateKey(key)
		cli.CheckErrf("unmarshaling private key: %v", err)
		id, err := core.IDFromPrivateKey(priv)
		cli.CheckErrf("getting peer id: %v", err)

		signingToken := hex.EncodeToString([]byte(id))

		fmt.Printf(`Bidbot needs a signature from a miner wallet address to authenticate bids.

1. Sign this token with an address from your miner owner Lotus wallet address:

    lotus wallet sign [owner-address] %s

2. Start listening for deal auctions using the wallet address and signature from step 1:

    bidbot daemon --storage-provider-id [id] 
                  --wallet-addr-sig [signature] 
		  --lotus-miner-api-maddr [lotus-miner-api-maddr] 
		  --lotus-miner-api-token [lotus-miner-api-token-with-write-access]
		  --deal-start-window [correct-deal-start-epoch-window-for-your-miner]

Note: In the event you win an auction, you must use this wallet address to make the deal(s).

Good luck!
`, signingToken)
	},
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run a network-connected bidding bot",
	Long:  "Run a network-connected bidding bot that listens for and bids on storage deal auctions.",
	Args:  cobra.ExactArgs(0),
	PersistentPreRun: func(c *cobra.Command, args []string) {
		cli.ExpandEnvVars(v, v.AllSettings())
		err := cli.ConfigureLogging(v, []string{
			cliName,
			"bidbot/service",
			"bidbot/store",
			"bidbot/datauri",
			"bidbot/api",
			"bidbot/lotus",
			"psrpc",
			"psrpc/peer",
			"psrpc/mdns",
		})
		cli.CheckErrf("setting log levels: %v", err)
	},
	Run: func(c *cobra.Command, args []string) {
		log.Infof("bidbot %s", buildinfo.Summary())
		storageProviderID := v.GetString("storage-provider-id")
		if storageProviderID == "" {
			storageProviderID = v.GetString("miner-addr") // fallback to support existing config
		}
		if storageProviderID == "" {
			cli.CheckErr(errors.New("--storage-provider-id is required. See 'bidbot help init' for instructions"))
		}
		if !validStorageProviderID.MatchString(storageProviderID) {
			cli.CheckErr(errors.New("--storage-provider-id should be in the form of f0xxxx"))
		}

		if v.GetString("wallet-addr-sig") == "" {
			cli.CheckErr(errors.New("--wallet-addr-sig is required. See 'bidbot help init' for instructions"))
		}

		pconfig, err := peerflags.GetConfig(v, "BIDBOT_PATH", defaultConfigPath, false)
		cli.CheckErrf("getting peer config: %v", err)

		settings, err := cli.MarshalConfig(v, !v.GetBool("log-json"),
			"private-key", "wallet-addr-sig", "lotus-miner-api-token")
		cli.CheckErrf("marshaling config: %v", err)
		log.Infof("loaded config from %s: %s", v.ConfigFileUsed(), string(settings))

		fin := finalizer.NewFinalizer()
		repoPath := os.Getenv("BIDBOT_PATH")
		if repoPath == "" {
			repoPath = defaultConfigPath
		}
		store, err := dshelper.NewBadgerTxnDatastore(filepath.Join(repoPath, "bidstore"))
		cli.CheckErrf("creating datastore: %v", err)
		fin.Add(store)

		walletAddrSig, err := hex.DecodeString(v.GetString("wallet-addr-sig"))
		cli.CheckErrf("decoding wallet address signature: %v", err)

		lc, err := lotusclient.New(
			v.GetString("lotus-miner-api-maddr"),
			v.GetString("lotus-miner-api-token"),
			v.GetInt("lotus-api-conn-retries"),
			v.GetBool("fake-mode"),
		)
		cli.CheckErrf("creating lotus client: %v", err)
		fin.Add(lc)

		fc, err := filclient.New(v.GetString("lotus-gateway-url"), v.GetBool("fake-mode"))
		cli.CheckErrf("creating chain client: %v", err)
		fin.Add(fc)

		dealDataDirectory := os.Getenv("BIDBOT_DEAL_DATA_DIRECTORY")
		if dealDataDirectory == "" {
			dealDataDirectory = filepath.Join(defaultConfigPath, "deal_data")
		}

		var bytesLimiter limiter.Limiter = limiter.NopeLimiter{}
		if limit := v.GetString("running-bytes-limit"); limit != "" {
			lim, err := parseRunningBytesLimit(limit)
			cli.CheckErrf(fmt.Sprintf("parsing '%s': %%w", limit), err)
			bytesLimiter = lim
		}

		config := service.Config{
			Peer: pconfig,
			BidParams: service.BidParams{
				StorageProviderID:       storageProviderID,
				WalletAddrSig:           walletAddrSig,
				AskPrice:                v.GetInt64("ask-price"),
				VerifiedAskPrice:        v.GetInt64("verified-ask-price"),
				FastRetrieval:           v.GetBool("fast-retrieval"),
				DealStartWindow:         v.GetUint64("deal-start-window"),
				DealDataDirectory:       dealDataDirectory,
				DealDataFetchAttempts:   v.GetUint32("deal-data-fetch-attempts"),
				DealDataFetchTimeout:    v.GetDuration("deal-data-fetch-timeout"),
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
			BytesLimiter:              bytesLimiter,
			ConcurrentImports:         v.GetInt("concurrent-imports-limit"),
			SealingSectorsLimit:       v.GetInt("sealing-sectors-limit"),
			PricingRules:              pricing.EmptyRules{},
			PricingRulesDefaultReject: v.GetBool("cid-gravity-default-reject"),
		}
		if cidGravityKey := v.GetString("cid-gravity-key"); cidGravityKey != "" {
			config.PricingRules = pricing.NewCIDGravityRules(cidGravityKey)
		}
		serv, err := service.New(config, store, lc, fc)
		cli.CheckErrf("starting service: %v", err)
		fin.Add(serv)

		err = serv.Subscribe(true)
		cli.CheckErrf("subscribing to deal auction feed: %v", err)

		api, err := httpapi.NewServer(":"+v.GetString("http-port"), serv)
		cli.CheckErrf("creating http API server: %v", err)
		fin.Add(api)

		cli.HandleInterrupt(func() {
			cli.CheckErr(fin.Cleanupf("closing service: %v", nil))
		})
	},
}

var idCmd = &cobra.Command{
	Use:   "id",
	Short: "shows the id, public key and addresses of the bidbot",
	Args:  cobra.ExactArgs(0),
	Run: func(c *cobra.Command, args []string) {
		res, err := http.Get(urlFor("id"))
		cli.CheckErr(err)
		defer func() {
			err := res.Body.Close()
			cli.CheckErr(err)
		}()
		b, _ := ioutil.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			log.Fatalf("%s: %s", res.Status, string(b))
		}
		fmt.Println(string(b))
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "shows current version of bidbot",
	Args:  cobra.ExactArgs(0),
	Run: func(c *cobra.Command, args []string) {
		local := buildinfo.Summary()
		fail := func(format string, vars ...interface{}) {
			fmt.Printf("local%s\n", local)
			log.Fatalf(format, vars...)
		}
		res, err := http.Get(urlFor("version"))
		if err != nil {
			fail(err.Error())
		}
		defer func() {
			err := res.Body.Close()
			if err != nil {
				fail(err.Error())
			}
		}()
		b, _ := ioutil.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			fail("%s: %s", res.Status, string(b))
		}
		remote := string(b)
		if remote == local {
			fmt.Println(local)
			return
		}
		fmt.Println("WARNING! You local and remote version don't match:")
		fmt.Printf("local%s\n", local)
		fmt.Printf("\ndaemon%s\n", remote)
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
			cli.CheckErr(err)
			fmt.Println(string(b))
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.DiscardEmptyColumns)
		for i, bid := range bids {
			if i == 0 {
				for _, field := range dealsListFields {
					_, err := fmt.Fprintf(w, "%s\t", field)
					cli.CheckErr(err)
				}
				_, err := fmt.Fprintln(w, "")
				cli.CheckErr(err)
			}
			value := reflect.ValueOf(bid)
			for _, field := range dealsListFields {
				_, err := fmt.Fprintf(w, "%v\t", value.FieldByName(field))
				cli.CheckErr(err)
			}
			_, err := fmt.Fprintln(w, "")
			cli.CheckErr(err)
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
			cli.CheckErr(err)
			fmt.Println(string(b))
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', 0)
		typ := reflect.TypeOf(bid)
		value := reflect.ValueOf(bid)
		for i := 0; i < typ.NumField(); i++ {
			_, err := fmt.Fprintf(w, "%s:\t%v\n", typ.Field(i).Name, value.Field(i))
			cli.CheckErr(err)
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
		cli.CheckErr(err)
		defer func() {
			err := res.Body.Close()
			cli.CheckErr(err)
		}()
		if _, err = io.Copy(os.Stdout, res.Body); err != io.EOF {
			cli.CheckErr(err)
		}
	},
}

func main() {
	cli.CheckErr(rootCmd.Execute())
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
	cli.CheckErr(err)
	defer func() {
		err := res.Body.Close()
		cli.CheckErr(err)
	}()
	if res.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(res.Body)
		log.Fatalf("%s: %s", res.Status, string(b))
	}
	decoder := json.NewDecoder(res.Body)
	err = decoder.Decode(&bids)
	cli.CheckErr(err)
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
	return limiter.NewRunningTotalLimiter(nBytes, d), nil
}
