package datauri

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/ipld/go-car"
	"github.com/textileio/bidbot/lib/auction"
	golog "github.com/textileio/go-log/v2"
)

var (
	log = golog.Logger("bidbot/datauri")

	// ErrSchemeNotSupported indicates a given URI scheme is not supported.
	ErrSchemeNotSupported = errors.New("scheme not supported")

	// ErrCarFileUnavailable indicates a given URI points to an unavailable car file.
	ErrCarFileUnavailable = errors.New("car file unavailable")

	// ErrInvalidCarFile indicates a given URI points to an invalid car file.
	ErrInvalidCarFile = errors.New("invalid car file")
)

// URI describes a data car file for a storage deal.
type URI interface {
	fmt.Stringer
	Cid() cid.Cid
	Write(context.Context, io.Writer) error
	Validate(ctx context.Context) error
}

// NewFromSources returns a new URI from the given sources.
func NewFromSources(payloadCid string, sources auction.Sources) (URI, error) {
	if sources.CARURL != nil {
		return NewURI(payloadCid, sources.CARURL.URL.String())
	}
	return nil, errors.New("not implemented")
}

// NewURI returns a new URI for the given string uri.
// ErrSchemeNotSupported is returned if the scheme is not supported.
func NewURI(payloadCid string, uri string) (URI, error) {
	id, err := cid.Parse(payloadCid)
	if err != nil {
		return nil, fmt.Errorf("parsing data cid: %v", err)
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("parsing uri '%s': %v", uri, err)
	}
	switch parsed.Scheme {
	case "http", "https":
		return &HTTPURI{uri: uri, cid: id}, nil
	default:
		return nil, fmt.Errorf("parsing uri '%s': %w", uri, ErrSchemeNotSupported)
	}
}

// HTTPURI is used to get http/https resources.
type HTTPURI struct {
	uri string
	cid cid.Cid
}

// Cid returns the data cid referenced by the uri.
func (u *HTTPURI) Cid() cid.Cid {
	return u.cid
}

// Validate checks the integrity of the car file.
// The cid associated with the uri must be the one and only root of the car file.
func (u *HTTPURI) Validate(ctx context.Context) error {
	res, err := u.getRequest(ctx)
	if err != nil {
		return fmt.Errorf("get request: %v", err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			log.Errorf("closing http get request: %v", err)
		}
	}()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("http request returned bad status %d: %w", res.StatusCode, ErrCarFileUnavailable)
	}

	log.Debugf("validating car file uri: %s", u.uri)
	if _, err := validateCarHeader(u.cid, res.Body); err != nil {
		return fmt.Errorf("validating car header: %w", err)
	}
	return nil
}

// Write the uri's car file to writer.
func (u *HTTPURI) Write(ctx context.Context, writer io.Writer) error {
	res, err := u.getRequest(ctx)
	if err != nil {
		return fmt.Errorf("get request: %v", err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			log.Errorf("closing http get request: %v", err)
		}
	}()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("http request returned bad status %d: %w", res.StatusCode, ErrCarFileUnavailable)
	}

	log.Debugf("validating car file uri: %s", u.uri)
	r, err := validateCarHeader(u.cid, res.Body)
	if err != nil {
		return fmt.Errorf("validating car header: %w", err)
	}

	if _, err := io.Copy(writer, r); err != nil {
		return fmt.Errorf("writing http get response: %v", err)
	}
	return nil
}

// String returns the uri as a string.
func (u *HTTPURI) String() string {
	return u.uri
}

func (u *HTTPURI) getRequest(ctx context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u.uri, nil)
	if err != nil {
		return nil, fmt.Errorf("building http request: %v", err)
	}
	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending http request: %v", err)
	}
	return res, nil
}

// validateCarHeader ensures cid is the one and only root of the car file.
func validateCarHeader(root cid.Cid, reader io.Reader) (io.Reader, error) {
	buf := bufio.NewReader(reader)
	phb, hb, err := ldRead(buf)
	if err != nil {
		return nil, fmt.Errorf("reading car header: %v", err)
	}
	var ch car.CarHeader
	if err := cbor.DecodeInto(hb, &ch); err != nil {
		return nil, fmt.Errorf("decoding car header: %v", err)
	}

	if len(ch.Roots) != 1 {
		return nil, fmt.Errorf("car file must have only one root: %w", ErrInvalidCarFile)
	}
	if !ch.Roots[0].Equals(root) {
		return nil, fmt.Errorf("car file root does not match uri: %w", ErrInvalidCarFile)
	}

	return io.MultiReader(bytes.NewReader(phb), bytes.NewReader(hb), buf), nil
}

// modified from https://github.com/ipld/go-car/blob/master/util/util.go#L102
// to return the header length prefix.
func ldRead(r *bufio.Reader) ([]byte, []byte, error) {
	if _, err := r.Peek(1); err != nil { // no more blocks, likely clean io.EOF
		return nil, nil, err
	}
	l, err := binary.ReadUvarint(r)

	if err != nil {
		if err == io.EOF {
			return nil, nil, io.ErrUnexpectedEOF // don't silently pretend this is a clean EOF
		}
		return nil, nil, err
	}

	buf := make([]byte, l)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, nil, err
	}

	ubuf := make([]byte, 1)
	_ = binary.PutUvarint(ubuf, l)

	return ubuf, buf, nil
}
