module github.com/sxwebdev/xconfig/tests/integration

go 1.26

require (
	github.com/go-playground/validator/v10 v10.30.3
	github.com/sxwebdev/xconfig v0.5.0
	github.com/sxwebdev/xconfig/decoders/xconfigyaml v0.0.0
)

require (
	github.com/gabriel-vasile/mimetype v1.4.15 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)

replace github.com/sxwebdev/xconfig => ../../

replace github.com/sxwebdev/xconfig/decoders/xconfigyaml => ../../decoders/xconfigyaml
