# bidbot

# broker-core

[![Made by Textile](https://img.shields.io/badge/made%20by-Textile-informational.svg)](https://textile.io)
[![Chat on Slack](https://img.shields.io/badge/slack-slack.textile.io-informational.svg)](https://slack.textile.io)
[![standard-readme compliant](https://img.shields.io/badge/readme%20style-standard-brightgreen.svg)](https://github.com/RichardLitt/standard-readme)

Join us on our [public Slack channel](https://slack.textile.io/) for news, discussions, and status updates. [Check out our blog](https://blog.textile.io/) for the latest posts and announcements.

## Table of Contents

- [Background](#background)
- [Install](#install)
- [Getting Started](#getting-started)
  - [Miners: Run a `bidbot`](#miners-run-a-bidbot)
  - [Running locally with some test data](#running-locally-with-some-test-data)
  - [Step to deploy a daemon](#steps-to-deploy-a-daemon)
- [Contributing](#contributing)
- [Changelog](#changelog)
- [License](#license)

## Background

Bidbot is a Filecoin Network sidecar for miners to bid in storage deal auctions.

## Getting Started

1. [Install Go 1.15 or newer](https://golang.org/doc/install)
2. `git clone https://github.com/textileio/bidbot.git`
3. `cd bidbot`
4. `make install`
5. `bidbot init`
6. The output from step 5 will ask you to sign a token with the owner address of your miner.
7. Configure your _ask price_, other bid settings, and auction filters. See `bidbot help daemon` for details. You can edit the configuration file generated in step 5 or use the equivalent flag for any given option.
8. Use the signature you generated in step 6 to start the daemon: `bidbot daemon --miner-addr [address] --wallet-addr-sig [signature]`
9. Good luck! Your `bidbot` will automatically bid in open deal auctions. If it wins an auction, the broker will automatically start making a deal with the Lotus wallet address used in step 6.   

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
