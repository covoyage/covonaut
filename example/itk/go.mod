module github.com/covoyage/covonaut/example/itk

go 1.25.0

require (
	github.com/covoyage/covonaut v0.0.0-00010101000000-000000000000
	google.golang.org/protobuf v1.36.11
)

require github.com/gorilla/websocket v1.5.3 // indirect

replace github.com/covoyage/covonaut => ../..
