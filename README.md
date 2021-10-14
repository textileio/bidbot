# bidbot

Bidbot is a Filecoin Network sidecar for storage providers to bid in storage deal auctions.

[![Made by Textile](https://img.shields.io/badge/made%20by-Textile-informational.svg)](https://textile.io)
[![Chat on Slack](https://img.shields.io/badge/slack-slack.textile.io-informational.svg)](https://slack.textile.io)
[![standard-readme compliant](https://img.shields.io/badge/readme%20style-standard-brightgreen.svg)](https://github.com/RichardLitt/standard-readme)

Join us on our [public Slack channel](https://slack.textile.io/) for news, discussions, and status updates. [Check out our blog](https://blog.textile.io/) for the latest posts and announcements.

## Table of Contents

- [Installation](#installation)
- [What is the Auctioneer](#what-is-an-auctioneer)
- [How do I connect with the system?](#how-do-i-connect-with-the-system)
- [CID gravity integration](#cid-gravity-integration)
- [Useful `bidbot` commands](#useful-bidbot-operational-commands)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [Changelog](#changelog)
- [License](#license)

## Installation
-   **Prebuilt package**: See [release assets](https://github.com/textileio/bidbot/releases/latest)
-   **Docker image**: See the `latest` tag on [Docker Hub](https://hub.docker.com/r/textile/bidbot/tags)
-   **Build from the source** ([Go 1.15 or newer](https://golang.org/doc/install)):
```bash
git clone https://github.com/textileio/bidbot.git
cd bidbot
make install
```

## What is an Auctioneer?

First, we must understand that `bidbot` is a piece that's part of a bigger picture: the storage auction system.
The storage auction system (Auctioneer) is a system that receives data from multiple clients, aggregates them, and makes it available for storage providers to store. The Auctioneer has several unique properties that make it different than your typical Filecoin client.

1. Storage providers connect to the Auctioneer to discover available data payloads awaiting deals.
2. Storage providers bid on deals in real time to offer the best storage solution for each payload.
3. All deals are offline deals, not online deals. Storage providers download the data to create the deal.

Currently the Auctioneer is creating a slow stream of auctions, so you might see _nothing happening_ when running `bidbot`. This is expected for some time, so unless you see errors in your logs you can leave the daemon running.

### How it works

Whenever the Auctioneer has aggregates a minimum threshold of data, it attempts to make deals with storage providers. Data payloads awaiting storage is described by the usual attributes you're familiar with in Filecoin:

- PayloadCid (cid)
- PieceCid (cid)
- DealSize (int64 in bytes)
- VerifiedDeal (bool)

To initiate the storage of the new payload, the Auctioneer creates an *auction*. The *auction* is published to all connected storage providers to notify them that a new payload is awaiting storage on Filecoin.

To understand better what this is about, consider the following chat:

- [Auctioneer]: "Hey storage providers, there's a new Auction for a dataset with size X, PayloadCid X, PieceCid X, and PieceSize X. This data will be created with a verified deal. Who's interested?"
- [Provider-A]: "Me! Here's my Bid: price-per-epoch=0.0001FIL and I promise to accept a deal proposal with *DealStartEpoch=XXXX".*
- [Provider-B]: Hey, me too! My price is 0.00000001FIL, and I promise to accept a deal proposal with DealStartepoch=YYYYY".
- [Provider-C]: Uhm, I'm too busy now... I won't bid here and just let this pass. Maybe in the next auction!

Storage providers will send *bids* to show their interest in participating in the auction. Bids provide the Auctioneer multiple pieces of information (i.e. price, start epoch, etc) about the provider's storage intent. Unlike the chat based auction, storage providers connected to the Auctioneer can configure bidding to happen automatically as fast as their infrastructure can handle. 

The Auctioneer collects all bids from connected storage providers and chooses one or multiple winners. The winning algorithm aims to maximize deal success rate for storage clients, while being fair to all storage providers, big or small, new or established. It is open and subject to continuous evolving based on community feedback. Anyone can join the biweekly [Auction Governance meeting](https://textile.notion.site/Auction-Governance-2eb1acae8a204b6e8dbf72752255a008) to contribute. As a rules of thumb, you can expect that the following facts will help winning an auction:

-- Provide a low price per epoch. Verified deals need to be zero-priced for now.
-- Provide a low DealStartEpoch, and confirm the winning deal on chain as quick as possible.
- Maintain a high deal success from past auctions.

If you win an auction, you will be notified about it! If that weren't the case, most probably you'll have better luck in the next auction!

The Auctioneer relies on offline deals to make deals with storage providers. After you win an auction, your bidbot will pull the deal data from the client. You can think of this flow as an *automatic* offline deal setup where instead of receiving hard drives, you'll pull the data from someplace and then receive the offline-deal proposal.

## How do I connect with the system?

The hypothetical scenario in the previous section obviously didn't explain how you would really send bids or interact with the Auctioneer. The following diagram will help to get a better picture:

![auctioneer + bidbots](https://user-images.githubusercontent.com/1369696/135887323-6b31cc1c-81e6-422e-9169-f09326680b69.png)

To connect to the system, you will install `bidbot`, a daemon that you run on your infrastructure. 

The `bidbot` talks to the Auctioneer through libp2p pubsub topics. This is the same technology used by many `libp2p` applications such as `lotus`. Through these topics, your `bidbot` daemon will receive new *auctions* and can send *bids* to participate.

Whenever you win an auction, your `lotus-miner` will receive the deal proposal for the corresponding data so you can accept it, and a *ProposalCid* is generated. After acceptance, your `bidbot` will automatically download the *PayloadCid* data via the URL or IPFS multiaddrs given by the client, and import it as you generally do with offline deals, simulating the following commands you might be aware of: `lotus-miner storage-deals import-data <dealCid> <carFilePath>`

The `carFilePath` is the CAR file that `bidbot` generated after downloading the deals data. The *dealCid* is the *ProposalCid* of the proposal you previously accepted from the Auctioneer.

There's nothing more to do than the usual flow of deal execution steps from that moment forward. What's important is that the deal becomes active on-chain before the *StartDealEpoch* you promised in your bid!

### How do I run `bidbot`?

Here're the steps to do it:

0. [Install Go 1.15 or newer](https://golang.org/doc/install)
1. Make sure `$GOPATH/bin` is in your `$PATH`, i.e., `export PATH=$GOPATH/bin:$PATH`.
2. `bidbot init`.
3. The output will ask you to sign a token with the owner address of your miner.
4. Configure your _ask price_, other bid settings, and auction filters. See `bidbot help daemon` for details. You can edit the configuration file generated by `bidbot init` (`~/.bidbot/config`) or use the equivalent flag for any given option.
5. Use the signature you generated above to start the daemon as indicated by step 2. output.
6. Good luck! Your `bidbot` will automatically bid in open deal auctions. If it wins an auction, the Auctioneer will automatically start making a deal with the Lotus miner address used in step 4.

**Important configuration considerations for step 4:**
- Bidbot will store the downloaded CAR files in `~/.bidbot/deal_data` by default. This directory should be accessible by your Lotus-miner for the CAR import step to work correctly. The default download folder would probably work if you run `bidbot` in the same host as your Lotus node. If you want to run `bidbot` and the Lotus node in separate hosts, you should set up a shared volume and set BIDBOT_DEAL_DATA_DIRECTORY environment variable on the `bidbot` host to change the download folder target. The shared volume should be mounted to the Lotus node host as the very same full path as the `bidbot` host.
- Your `--ask-price` and `--verified-ask-price` value should match your markets node configuration.
- You should set correctly your `--deal-start-window` value to be a bit larger than your miner node configuration, to cover the time required to download and import data. If this value is incorrectly set, your miner node will reject proposals because the `DealStartEpoch` attribute is too soon for you to accept.
- The `--lotus-miner-api-token` should have `write` access.
- Consider using the `--sealing-sectors-limit` flag to allow `bidbot` pause bidding if you have more than the specified number of sectors sealing.

## How does this fit in my current miner infrastructure?

The following diagram shows how bidbot, auctioneer, and your lotus-miner interact:

![auctioneer, bidbot, and lotus-miner](https://user-images.githubusercontent.com/1369696/135889226-19cf7830-8ed9-495f-a685-76e6f63c8b47.png)

**Note**: you can install `bidbot` on the same host as your miner daemon, but there's a reasonable chance that you might want to avoid that for security reasons.

### OK, I've it running, what I should see?

First, congratulation on being connected! When starting your `bidbot` daemon, you should see the following lines appearing in your console:

```go
2021-05-28T17:33:30.814-0300    INFO    mpeer   marketpeer/marketpeer.go:148    marketpeer 12D3KooWPX8uutGrghEYLt1i9EPmLmwSUYtYD3BN1tvARK5YXDXV is online
2021-05-28T17:33:30.814-0300    DEBUG   mpeer   marketpeer/marketpeer.go:149    marketpeer addresses: [/ip4/192.168.1.27/udp/4002/quic /ip4/127.0.0.1/udp/4002/quic /ip4/192.168.1.27/tcp/4002 /ip4/127.0.0.1/tcp/4002]
2021-05-28T17:33:30.814-0300    INFO    mpeer   marketpeer/marketpeer.go:164    mdns was enabled (interval=1s)
2021-05-28T17:33:30.814-0300    INFO    bidbot/service  service/service.go:143  service started
2021-05-28T17:33:31.875-0300    INFO    mpeer   marketpeer/marketpeer.go:184    peer was bootstapped
2021-05-28T17:33:31.876-0300    INFO    bidbot/service  service/service.go:191  subscribed to the deal auction feed
```

It's important to check that you see the last line ("subscribed to the deal auction feed") in your log output. This means you're connected to the Auctioneer successfully.

After some time, you'll see some activity in the log output, such as:

```go
2021-05-28T17:34:26.467-0300    DEBUG   bidbot/service  service/service.go:202  /textile/auction/0.0.1 received auction from 12D3KooWRhbmhcWGB84qPehvZvtVyzW8qNkdbcX2gGidhAmJBzhi                                                                                                         
2021-05-28T17:34:26.467-0300    DEBUG   mpeer/pubsub    pubsub/pubsub.go:245    /textile/auction/0.0.1/12D3KooWRhbmhcWGB84qPehvZvtVyzW8qNkdbcX2gGidhAmJBzhi/acks ack peer event: 12D3KooWRhbmhcWGB84qPehvZvtVyzW8qNkdbcX2gGidhAmJBzhi JOINED                                              
2021-05-28T17:34:26.467-0300    INFO    bidbot/service  service/service.go:215  received auction 01f6taxy1v33a8pmdttas03de3 from 12D3KooWRhbmhcWGB84qPehvZvtVyzW8qNkdbcX2gGidhAmJBzhi:                                                                                                    
{                                                                                                                                                                                                                                                                                         
  "id": "01f6taxy1v33a8pmdttas03de3",                                                                                                                                                                                                                                                     
  "deal_size": 524288,                                                                                                                                                                                                                                                                    
  "deal_duration": 1051200,                                                                                                                                                                                                                                                               
  "ends_at": {                                                                                                                                                                                                                                                                            
    "seconds": 1622234076,                                                                                                                                                                                                                                                                
    "nanos": 311991847                                                                                                                                                                                                                                                                    
  }                                                                                                                                                                                                                                                                                       
}
```

There you can see that your `bidbot` was notified of a new auction 🎉

You'll also see bids that are proposed in the auction:

```go
2021-05-28T17:53:03.925-0300    INFO    bidbot/service  service/service.go:269  bidding in auction 01f6tc0ygw6bpyj00e0k17secb from 12D3KooWRhbmhcWGB84qPehvZvtVyzW8qNkdbcX2gGidhAmJBzhi: 
{                                                                     
  "auction_id": "01f6tc0ygw6bpyj00e0k17secb",                                                                                                
  "storage_provider_id": "f02222",                                             
  "wallet_addr_sig": "***",
  "ask_price": 10,                                                    
  "verified_ask_price": 10,                                           
  "start_epoch": 800506,                                              
  "fast_retrieval": true                                              
}
```

If you win an auction, you should see a log line similar to:

```go
2021-05-28T17:53:13.790-0300    DEBUG   bidbot/service  service/service.go:225  /textile/auction/0.0.1/12D3KooWPX8uutGrghEYLt1i9EPmLmwSUYtYD3BN1tvARK5YXDXV/wins received win from 12D3KooWRhbmhcWGB84qPehvZvtVyzW8qNkdbcX2gGidhAmJBzhi
```

## CID gravity integration

[CID gravity](https://www.cidgravity.com) is a tool for storage providers to manage clients and price tiers. If integrated, bidbot can bid based on the configuration there, rather than locally configured `--ask-price` and `--verified-ask-price`. There are only two parameters involved.

* `--cid-gravity-key`. You should be able to generate one by clicking the "Integrations" menu item from the CID gravity console.
* `--cid-gravity-strict`. By default, if bidbot can not reach the CID gravity API for some reason, it bids based on the locally configured price. If you want it to stop bidding in that case, set this to true.

Bidbot deals are from either Textile's address, `f144zep4gitj73rrujd3jw6iprljicx6vl4wbeavi`, or auction client's address. The address is shown when you `bidbot deals list` or `bidbot deals show`. If you want to have specific pricing model for bidbot deals, create a client with the pricing model in CID gravity console, then add the client addresses you care about.

## Useful `bidbot` operational commands

If you have `bidbot` daemon running, we recommend you explore the following available commands:

- `bidbot download <payload-cid> <data-uri>`: This command allows you to download a CAR file to the `bidbot` download folder. This can be helpful to test downloads or manually re-download a particular CAR from a won auction (if that's necessary).
- `bidbot deals list`: This command shows you a list of your won auctions and some summary of which stage you're in the process.
- `bidbot deals show <bid-id>`: This command shows detailed information about a won auction.
- `bidbot pause` and `bidbot resume`: Useful when you use script to manage your miner load.
- `bidbot install-service`: Install bidbot as a systemd service. Need to run with `sudo`.

Do you think `bidbot` can have other commands that would make your life easier? We're interested in knowing about that!

## Troubleshooting

Problems can arise, especially when you are new to bidbot. Here are some common ones you may encounter:

1. Lots of deals in `awaiting_proposal` status when you `bidbot deals list`. Most probably either
    * Your miner can not be reached. Do the following to check(credit to @TippyFlitsUK)
        - Get your miner peer ID - `lotus-miner net id`
        - Try to find your own ID  - `lotus net findpeer YOUR_MINER_PEER_ID`
        - Try several times with the Glif node that Bidbot uses - `FULLNODE_API_INFO="https://api.node.glif.io" lotus net findpeer YOUR_MINER_PEER_ID`.
    * Or you set too small a `--deal-start-window`. You would see something like `deal rejected: cannot seal a sector before ...` in your miner log. Set your `deal-start-window` to be a few hours longer than `ExpectedSealDuration` in your miner config.

1. Too many deals flood in, exceeding you sealing capacity. Two ways to throttle.
    * `--running-bytes-limit` to limit the total bytes bidbot processes in a period of time. 
    * `--sealing-sectors-limit` to pause bidding if the miner node has more than desired sectors in sealing.

1. We've seen a case when bidbot importing data long after winning the bids, it got errors like "normal shutdown of state machine" or "given data does not match expected commP ..." even though the real problem was the deal start epoch being exceeded. When in doubt, check your `lotus-miner` logs around that time for the real reason.

It is also suggested to set `--log-debug=true` and keep the logs when you want to seek help from Textile or the community.

## Contributing

Pull requests and bug reports are very welcome ❤️

This repository falls under the Textile [Code of Conduct](./CODE_OF_CONDUCT.md).

Feel free to get in touch by:
-   [Opening an issue](https://github.com/textileio/bidbot/issues/new)
-   Joining the [public Slack channel](https://slack.textile.io/)
-   Sending an email to contact@textile.io

## Changelog

A changelog is published along with each [release](https://github.com/textileio/bidbot/releases).

## License

[MIT](LICENSE)
