package dshelper

import (
	"os"

	"github.com/textileio/bidbot/lib/dshelper/txndswrap"
	badger "github.com/textileio/go-ds-badger3"
)

// NewBadgerTxnDatastore returns a new txndswrap.TxnDatastore backed by Badger.
func NewBadgerTxnDatastore(repoPath string) (txndswrap.TxnDatastore, error) {
	if err := os.MkdirAll(repoPath, os.ModePerm); err != nil {
		return nil, err
	}
	return badger.NewDatastore(repoPath, &badger.DefaultOptions)
}
