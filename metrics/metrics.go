package metrics

import "go.opentelemetry.io/otel/metric/global"

var _ = global.Meter("bidbot")
