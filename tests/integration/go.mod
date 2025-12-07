module github.com/sxwebdev/xconfig/tests/integration

go 1.24

require (
	github.com/google/go-cmp v0.7.0
	github.com/sxwebdev/xconfig v0.0.0
	github.com/sxwebdev/xconfig/decoders/xconfigyaml v0.0.0
)

require github.com/goccy/go-yaml v1.18.0 // indirect

replace github.com/sxwebdev/xconfig => ../../

replace github.com/sxwebdev/xconfig/decoders/xconfigyaml => ../../decoders/xconfigyaml
