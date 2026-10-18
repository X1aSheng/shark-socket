module peerbench

go 1.26.9

// The shark-socket arm must be the tree under test, not a published version:
// a peer benchmark that measures a tag is a press release, not a measurement.
require github.com/X1aSheng/shark-socket v0.0.0

require (
	github.com/cloudwego/netpoll v0.7.5
	github.com/panjf2000/gnet/v2 v2.10.0
)

require (
	github.com/bytedance/gopkg v0.1.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/gopkg v0.1.4 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/panjf2000/ants/v2 v2.12.1 // indirect
	github.com/pion/dtls/v3 v3.1.12 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v5 v5.0.0 // indirect
	github.com/quic-go/quic-go v0.59.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	go.etcd.io/bbolt v1.4.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/X1aSheng/shark-socket => ../..
