package logging

import (
	"encoding/json"

	logging "github.com/textileio/go-log/v2"
	"go.uber.org/zap/zapcore"
)

// SetLogLevels sets levels for the given systems.
func SetLogLevels(systems map[string]logging.LogLevel) error {
	for sys, level := range systems {
		l := zapcore.Level(level)
		if sys == "*" {
			for _, s := range logging.GetSubsystems() {
				if err := logging.SetLogLevel(s, l.CapitalString()); err != nil {
					return err
				}
			}
		}
		if err := logging.SetLogLevel(sys, l.CapitalString()); err != nil {
			return err
		}
	}
	return nil
}

// MustJSONIndent is an errorless method to json indent structs so they can be printed
// in log.XXXf in a single line.
func MustJSONIndent(b interface{}) string {
	jsn, _ := json.MarshalIndent(b, "", " ")
	return string(jsn)
}
