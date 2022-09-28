package testdata

import _ "embed"

var (
	//go:embed smd-service-config.yaml
	ServiceConfigYAML string

	//go:embed smd-service-live.yaml
	ServiceLiveYAML string

	//go:embed smd-service-config-2-ports.yaml
	ServiceConfigWith2Ports string

	//go:embed smd-service-live-with-type.yaml
	LiveServiceWithTypeYAML string

	//go:embed smd-service-config-ports.yaml
	ServiceConfigWithSamePortsYAML string

	//go:embed smd-deploy-live.yaml
	DeploymentLiveYAML string

	//go:embed smd-deploy-config.yaml
	DeploymentConfigYAML string

	//go:embed openapiv2.bin
	OpenAPIV2Doc []byte
)
