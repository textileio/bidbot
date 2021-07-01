package finalizer

import (
	"context"
	"fmt"
	"io"

	"github.com/hashicorp/go-multierror"
)

// NewFinalizer returns a new Finalizer.
func NewFinalizer() *Finalizer {
	return &Finalizer{}
}

// Finalizer collects resources for convenient cleanup.
type Finalizer struct {
	resources []io.Closer
}

// Add one or more io.Closer to the finalizer.
func (r *Finalizer) Add(cs ...io.Closer) {
	r.resources = append(r.resources, cs...)
}

type noopCloser struct {
	f func()
}

func (np *noopCloser) Close() error {
	np.f()
	return nil
}

// AddFn one or more func() to the finalizer.
func (r *Finalizer) AddFn(fs ...func()) {
	for _, f := range fs {
		r.resources = append(r.resources, &noopCloser{f: f})
	}
}

// Cleanup closes all io.Closer and cancels all contexts with err.
func (r *Finalizer) Cleanup(err error) error {
	var errs []error
	for i := len(r.resources) - 1; i >= 0; i-- {
		// release resources in a reverse order
		if e := r.resources[i].Close(); e != nil {
			errs = append(errs, e)
		}
	}
	return multierror.Append(err, errs...).ErrorOrNil()
}

// Cleanupf closes all io.Closer and cancels all contexts with a formatted err.
func (r *Finalizer) Cleanupf(format string, err error) error {
	if err != nil {
		return r.Cleanup(fmt.Errorf(format, err))
	}
	return r.Cleanup(nil)
}

// NewContextCloser transforms context cancellation function to be used with finalizer.
func NewContextCloser(cancel context.CancelFunc) io.Closer {
	return &ContextCloser{cf: cancel}
}

// ContextCloser maps a context to io.Closer.
type ContextCloser struct {
	cf context.CancelFunc
}

// Close the context.
func (cc ContextCloser) Close() error {
	cc.cf()
	return nil
}
