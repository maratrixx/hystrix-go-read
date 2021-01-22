module github.com/imttx/hystrix-go

go 1.15

require (
	github.com/DataDog/datadog-go v4.2.0+incompatible
	github.com/cactus/go-statsd-client v0.0.0-00010101000000-000000000000
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/kr/pretty v0.1.0 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20200313005456-10cdbea86bc0
	github.com/smartystreets/goconvey v1.6.4
	github.com/stretchr/testify v1.6.1 // indirect
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127 // indirect
)

replace github.com/cactus/go-statsd-client => github.com/cactus/go-statsd-client/v4 v4.0.0
