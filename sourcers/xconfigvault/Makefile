.PHONY: integration-test integration-test-down

integration-test:
	docker compose up -d --wait
	VAULT_ADDR=http://localhost:8200 VAULT_TOKEN=test-root-token \
		go test -tags=integration -v -count=1 ./...
	docker compose down

integration-test-down:
	docker compose down
