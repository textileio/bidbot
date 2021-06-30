package dshelper

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/textileio/bidbot/lib/dshelper/txndswrap"
	badger "github.com/textileio/go-ds-badger3"
	mongods "github.com/textileio/go-ds-mongo"
)

// NewBadgerTxnDatastore returns a new txndswrap.TxnDatastore backed by Badger.
func NewBadgerTxnDatastore(repoPath string) (txndswrap.TxnDatastore, error) {
	if err := os.MkdirAll(repoPath, os.ModePerm); err != nil {
		return nil, err
	}
	return badger.NewDatastore(repoPath, &badger.DefaultOptions)
}

// NewMongoTxnDatastore returns a new txndswrap.TxnDatastore backed by MongoDB.
func NewMongoTxnDatastore(uri, dbName string) (txndswrap.TxnDatastore, error) {
	mongoCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	if uri == "" {
		return nil, fmt.Errorf("mongo uri is empty")
	}
	if dbName == "" {
		return nil, fmt.Errorf("mongo database name is empty")
	}
	ds, err := mongods.New(mongoCtx, uri, dbName)
	if err != nil {
		return nil, fmt.Errorf("opening mongo datastore: %s", err)
	}

	return ds, nil
}
