module github.com/afex/hystrix-go

go 1.15

require (
	github.com/DataDog/datadog-go v4.2.0+incompatible
	github.com/cactus/go-statsd-client v0.0.0-00010101000000-000000000000
	github.com/rcrowley/go-metrics v0.0.0-20200313005456-10cdbea86bc0
	github.com/smartystreets/goconvey v1.6.4
	github.com/stretchr/testify v1.6.1 // indirect
	go.uber.org/zap v1.16.0
)

replace github.com/cactus/go-statsd-client => github.com/cactus/go-statsd-client/v4 v4.0.0
