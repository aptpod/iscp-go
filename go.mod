module github.com/aptpod/iscp-go

go 1.19

require (
	github.com/AlekSi/pointer v1.2.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang/mock v1.6.0
	github.com/google/uuid v1.3.0
	github.com/gorilla/websocket v1.4.2
	github.com/lucas-clemente/quic-go v0.27.1-0.20220416130901-6d4a69418397
	github.com/marten-seemann/webtransport-go v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.7.1
	go.uber.org/goleak v1.1.10
	golang.org/x/oauth2 v0.0.0-20190226205417-e64efc72b421
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	nhooyr.io/websocket v1.8.6
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fsnotify/fsnotify v1.5.1 // indirect
	github.com/go-task/slim-sprig v0.0.0-20210107165309-348f09dbbbc0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.0.3 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/klauspost/compress v1.10.10 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/marten-seemann/qpack v0.2.1 // indirect
	github.com/marten-seemann/qtls-go1-18 v0.1.2 // indirect
	github.com/marten-seemann/qtls-go1-19 v0.1.0 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/nxadm/tail v1.4.8 // indirect
	github.com/onsi/ginkgo v1.16.5 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/crypto v0.0.0-20220411220226-7b82a4e95df4 // indirect
	golang.org/x/exp v0.0.0-20220722155223-a9213eeb770e // indirect
	golang.org/x/lint v0.0.0-20190930215403-16217165b5de // indirect
	golang.org/x/mod v0.6.0-dev.0.20220106191415-9b9b3d81d5e3 // indirect
	golang.org/x/net v0.0.0-20220624214902-1bab6f366d9e // indirect
	golang.org/x/sys v0.0.0-20220520151302-bc2c85ada10a // indirect
	golang.org/x/text v0.3.7 // indirect
	golang.org/x/tools v0.1.10 // indirect
	golang.org/x/xerrors v0.0.0-20220411194840-2f41105eb62f // indirect
	google.golang.org/appengine v1.4.0 // indirect
	google.golang.org/protobuf v1.26.0 // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c // indirect
)

replace github.com/lucas-clemente/quic-go => github.com/aptpod/quic-go v0.27.1-0.20220830052917-e85f75cb2bc9

replace github.com/marten-seemann/webtransport-go => github.com/hareku/webtransport-go v0.0.0-20221129070516-8cf51d8ef963
