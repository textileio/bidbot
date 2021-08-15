package main

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/textileio/bidbot/service/store"
)

func TestParseRunningBytesLimit(t *testing.T) {
	errorCases := []string{
		"",
		"/",
		"5/1y",
	}
	validCases := []string{
		"5kb/1m",
		"5 PiB/ 128h",
		"0 tib /128h",
	}
	for _, s := range errorCases {
		_, err := parseRunningBytesLimit(s)
		require.Error(t, err)
	}

	for _, s := range validCases {
		_, err := parseRunningBytesLimit(s)
		require.NoError(t, err)
	}
}

func TestDealsListFields(t *testing.T) {
	value := reflect.ValueOf(store.Bid{})
	// make sure the field names are up-to-date with the struct definition.
	for _, field := range dealsListFields {
		assert.True(t, value.FieldByName(field).IsValid())
	}
}

func TestStorageProviderIDRegexp(t *testing.T) {
	assert.True(t, validStorageProviderID.MatchString("f0001"))
	assert.True(t, validStorageProviderID.MatchString("t0001"))
	assert.False(t, validStorageProviderID.MatchString("f0f01"))
	assert.False(t, validStorageProviderID.MatchString("f1001"))
}
