module github.com/sxwebdev/xconfig/tests/integration

go 1.24.0

require (
	github.com/go-playground/validator/v10 v10.28.0
	github.com/google/go-cmp v0.7.0
	github.com/sxwebdev/xconfig v0.0.0
	github.com/sxwebdev/xconfig/decoders/xconfigyaml v0.0.0
)

require (
	github.com/gabriel-vasile/mimetype v1.4.10 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	golang.org/x/crypto v0.42.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

replace github.com/sxwebdev/xconfig => ../../

replace github.com/sxwebdev/xconfig/decoders/xconfigyaml => ../../decoders/xconfigyaml
