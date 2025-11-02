module github.com/sxwebdev/xconfig/tests/integration

go 1.23.0

require (
	github.com/sxwebdev/xconfig v0.0.0
	github.com/sxwebdev/xconfig/decoders/xconfigyaml v0.0.0
)

require github.com/goccy/go-yaml v1.18.0 // indirect

replace github.com/sxwebdev/xconfig => ../../

replace github.com/sxwebdev/xconfig/decoders/xconfigyaml => ../../decoders/xconfigyaml
