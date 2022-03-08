package txndswrap

import (
	"context"
	"strings"

	ds "github.com/ipfs/go-datastore"
	kt "github.com/ipfs/go-datastore/keytransform"
	dsq "github.com/ipfs/go-datastore/query"
	dse "github.com/textileio/go-datastore-extensions"
)

// Wrap wraps a TxDatastore with a namespace prefix.
func Wrap(child TxnDatastore, prefix string) *Datastore {
	parts := strings.Split(prefix, "/")
	prefixKey := ds.NewKey("/")
	for _, part := range parts {
		prefixKey = prefixKey.ChildString(part)
	}
	t := kt.PrefixTransform{Prefix: prefixKey}
	nds := &Datastore{
		child:        child,
		Datastore:    kt.Wrap(child, t),
		KeyTransform: t,
	}
	return nds
}

// TxnDatastore adds QueryExtensions to TxnDatastore.
type TxnDatastore interface {
	ds.TxnDatastore
	dse.DatastoreExtensions
}

// Datastore keeps a KeyTransform function.
type Datastore struct {
	child TxnDatastore
	ds.Datastore
	kt.KeyTransform
}

var _ dse.DatastoreExtensions = (*Datastore)(nil)

type txn struct {
	Txn dse.TxnExt
	ds  *Datastore
}

var _ dse.TxnExt = (*txn)(nil)

// NewTransaction returns a transaction wrapped by the selected namespace prefix.
func (d *Datastore) NewTransaction(readOnly bool) (ds.Txn, error) {
	return d.newTransaction(readOnly)
}

// NewTransactionExtended returns an extended transaction wrapped by the selected namespace prefix.
func (d *Datastore) NewTransactionExtended(readOnly bool) (dse.TxnExt, error) {
	return d.newTransaction(readOnly)
}

func (d *Datastore) newTransaction(readOnly bool) (dse.TxnExt, error) {
	t, err := d.child.NewTransactionExtended(readOnly)
	if err != nil {
		return nil, err
	}

	return &txn{Txn: t, ds: d}, nil
}

// QueryExtended queries the selected namespace prefix with extensions.
func (d *Datastore) QueryExtended(q dse.QueryExt) (dsq.Results, error) {
	return d.child.QueryExtended(q)
}

func (t *txn) Commit(ctx context.Context) error {
	return t.Txn.Commit(ctx)
}

func (t *txn) Discard(ctx context.Context) {
	t.Txn.Discard(ctx)
}

// Put stores the given value, transforming the key first.
func (t *txn) Put(ctx context.Context, key ds.Key, value []byte) (err error) {
	return t.Txn.Put(ctx, t.ds.ConvertKey(key), value)
}

// Delete removes the value for given key.
func (t *txn) Delete(ctx context.Context, key ds.Key) (err error) {
	return t.Txn.Delete(ctx, t.ds.ConvertKey(key))
}

// Get returns the value for given key, transforming the key first.
func (t *txn) Get(ctx context.Context, key ds.Key) (value []byte, err error) {
	return t.Txn.Get(ctx, t.ds.ConvertKey(key))
}

// Has returns whether the datastore has a value for a given key, transforming
// the key first.
func (t *txn) Has(ctx context.Context, key ds.Key) (exists bool, err error) {
	return t.Txn.Has(ctx, t.ds.ConvertKey(key))
}

// GetSize returns the size of the value named by the given key, transforming
// the key first.
func (t *txn) GetSize(ctx context.Context, key ds.Key) (size int, err error) {
	return t.Txn.GetSize(ctx, t.ds.ConvertKey(key))
}

// Query implements Query, inverting keys on the way back out.
func (t *txn) Query(ctx context.Context, q dsq.Query) (dsq.Results, error) {
	nq, cq := t.prepareQuery(dse.QueryExt{Query: q})

	qr, err := t.Txn.Query(ctx, cq.Query)
	if err != nil {
		return nil, err
	}
	return dsq.NaiveQueryApply(nq.Query, dsq.ResultsFromIterator(q, t.getIterator(qr))), nil
}

// QueryExtended implements QueryExtended, inverting keys on the way back out.
func (t *txn) QueryExtended(q dse.QueryExt) (dsq.Results, error) {
	nq, cq := t.prepareQuery(q)

	qr, err := t.Txn.QueryExtended(cq)
	if err != nil {
		return nil, err
	}
	return dsq.NaiveQueryApply(nq.Query, dsq.ResultsFromIterator(q.Query, t.getIterator(qr))), nil
}

// Split the query into a child query and a naive query. That way, we can make
// the child datastore do as much work as possible.
func (t *txn) prepareQuery(q dse.QueryExt) (naive, child dse.QueryExt) {
	// First, put everything in the child query. Then, start taking things
	// out.
	child = q

	// Always let the child handle the key prefix.
	child.Prefix = t.ds.ConvertKey(ds.NewKey(child.Prefix)).String()
	if child.SeekPrefix != "" {
		child.SeekPrefix = t.ds.ConvertKey(ds.NewKey(child.SeekPrefix)).String()
	}

	// Check if the key transform is order-preserving so we can use the
	// child datastore's built-in ordering.
	orderPreserving := false
	switch t.ds.KeyTransform.(type) {
	case kt.PrefixTransform, *kt.PrefixTransform:
		orderPreserving = true
	}

	// Try to let the child handle ordering.
orders:
	for i, o := range child.Orders {
		switch o.(type) {
		case dsq.OrderByValue, *dsq.OrderByValue,
			dsq.OrderByValueDescending, *dsq.OrderByValueDescending:
			// Key doesn't matter.
			continue
		case dsq.OrderByKey, *dsq.OrderByKey,
			dsq.OrderByKeyDescending, *dsq.OrderByKeyDescending:
			// if the key transform preserves order, we can delegate
			// to the child datastore.
			if orderPreserving {
				// When sorting, we compare with the first
				// Order, then, if equal, we compare with the
				// second Order, etc. However, keys are _unique_
				// so we'll never apply any additional orders
				// after ordering by key.
				child.Orders = child.Orders[:i+1]
				break orders
			}
		}

		// Can't handle this order under transform, punt it to a naive
		// ordering.
		naive.Orders = q.Orders
		child.Orders = nil
		naive.Offset = q.Offset
		child.Offset = 0
		naive.Limit = q.Limit
		child.Limit = 0
		break
	}

	// Try to let the child handle the filters.

	// don't modify the original filters.
	child.Filters = append([]dsq.Filter(nil), child.Filters...)

	for i, f := range child.Filters {
		switch f := f.(type) {
		case dsq.FilterValueCompare, *dsq.FilterValueCompare:
			continue
		case dsq.FilterKeyCompare:
			child.Filters[i] = dsq.FilterKeyCompare{
				Op:  f.Op,
				Key: t.ds.ConvertKey(ds.NewKey(f.Key)).String(),
			}
			continue
		case *dsq.FilterKeyCompare:
			child.Filters[i] = &dsq.FilterKeyCompare{
				Op:  f.Op,
				Key: t.ds.ConvertKey(ds.NewKey(f.Key)).String(),
			}
			continue
		case dsq.FilterKeyPrefix:
			child.Filters[i] = dsq.FilterKeyPrefix{
				Prefix: t.ds.ConvertKey(ds.NewKey(f.Prefix)).String(),
			}
			continue
		case *dsq.FilterKeyPrefix:
			child.Filters[i] = &dsq.FilterKeyPrefix{
				Prefix: t.ds.ConvertKey(ds.NewKey(f.Prefix)).String(),
			}
			continue
		}

		// Not a known filter, defer to the naive implementation.
		naive.Filters = q.Filters
		child.Filters = nil
		naive.Offset = q.Offset
		child.Offset = 0
		naive.Limit = q.Limit
		child.Limit = 0
		break
	}
	return
}

func (t *txn) getIterator(qr dsq.Results) dsq.Iterator {
	return dsq.Iterator{
		Next: func() (dsq.Result, bool) {
			r, ok := qr.NextSync()
			if !ok {
				return r, false
			}
			if r.Error == nil {
				r.Entry.Key = t.ds.InvertKey(ds.RawKey(r.Entry.Key)).String()
			}
			return r, true
		},
		Close: func() error {
			return qr.Close()
		},
	}
}
