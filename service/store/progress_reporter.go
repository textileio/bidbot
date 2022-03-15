package store

import "github.com/textileio/bidbot/lib/auction"

// ProgressReporter reports the progress processing a deal.
type ProgressReporter interface {
	StartFetching(bidID auction.BidID, attempts uint32)
	ErrorFetching(bidID auction.BidID, attempts uint32, err error)
	StartImporting(bidID auction.BidID, attempts uint32)
	EndImporting(bidID auction.BidID, attempts uint32, err error)
	Finalized(bidID auction.BidID)
	Errored(bidID auction.BidID, errrorCause string)
}

type nullProgressReporter struct{}

func (pr nullProgressReporter) StartFetching(bidID auction.BidID, attempts uint32)            {}
func (pr nullProgressReporter) ErrorFetching(bidID auction.BidID, attempts uint32, err error) {}
func (pr nullProgressReporter) StartImporting(bidID auction.BidID, attempts uint32)           {}
func (pr nullProgressReporter) EndImporting(bidID auction.BidID, attempts uint32, err error)  {}
func (pr nullProgressReporter) Finalized(bidID auction.BidID)                                 {}
func (pr nullProgressReporter) Errored(bidID auction.BidID, errorCause string)                {}
