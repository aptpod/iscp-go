module github.com/aptpod/iscp-go

go 1.20

require (
	github.com/AlekSi/pointer v1.2.0
	github.com/aptpod/iscp-proto v0.0.0-20230808235245-fada26057efa
	github.com/gogo/protobuf v1.3.2
	github.com/golang/mock v1.6.0
	github.com/google/uuid v1.3.0
	github.com/gorilla/websocket v1.4.2
	github.com/quic-go/quic-go v0.39.0
	github.com/quic-go/webtransport-go v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.8.0
	go.uber.org/goleak v1.1.10
	golang.org/x/oauth2 v0.0.0-20190226205417-e64efc72b421
	golang.org/x/sync v0.3.0
	nhooyr.io/websocket v1.8.10
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/pprof v0.0.0-20230821062121-407c9e7a662f // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/onsi/ginkgo/v2 v2.12.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quic-go/qpack v0.4.0 // indirect
	github.com/quic-go/qtls-go1-20 v0.3.4 // indirect
	go.uber.org/mock v0.3.0 // indirect
	golang.org/x/crypto v0.12.0 // indirect
	golang.org/x/exp v0.0.0-20221205204356-47842c84f3db // indirect
	golang.org/x/lint v0.0.0-20190930215403-16217165b5de // indirect
	golang.org/x/mod v0.12.0 // indirect
	golang.org/x/net v0.14.0 // indirect
	golang.org/x/sys v0.11.0 // indirect
	golang.org/x/text v0.12.0 // indirect
	golang.org/x/tools v0.12.0 // indirect
	google.golang.org/appengine v1.4.0 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/quic-go/quic-go => github.com/aptpod/quic-go v0.27.1-0.20231006092722-5ee357d2b287

replace github.com/quic-go/webtransport-go => github.com/aptpod/webtransport-go v0.0.0-20231006101523-12e614a3d68f
