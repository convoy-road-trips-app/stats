package otlp

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
)

// detectedAttributes returns the host, process and SDK attributes of this
// process, detected once with the OTel SDK detectors: host.name, process.pid,
// process.runtime.{name,version,description} and telemetry.sdk.{name,language,
// version}. The process command line, owner and executable path are left out
// on purpose, as they can hold secrets. A detector that fails only drops its
// own attributes.
var detectedAttributes = sync.OnceValue(func() []attribute.KeyValue {
	detected, _ := resource.New(context.Background(),
		resource.WithHost(),
		resource.WithProcessPID(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),
		resource.WithTelemetrySDK(),
	)
	if detected == nil {
		return nil
	}
	return detected.Attributes()
})
