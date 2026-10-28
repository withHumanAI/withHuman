.PHONY: all frontend fmt test hermes-plugin-test verify api-lint openapi-permissions deployment-check build smoke smoke-onboarding smoke-rbac

all: test build

frontend:
	cd frontend && npm ci && npm run build

fmt:
	gofmt -w cmd internal db domain application approvalblock authn entitlements loggermdc runtime singlebinary plugins

test: frontend hermes-plugin-test
	go test $(GOTESTFLAGS) ./...
	go vet ./...
	cd frontend && npm test

# The Hermes plugin's tests; the ones against a real Hermes run only when
# Hermes is importable.
hermes-plugin-test:
	cd plugins/hermes/tests && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest

verify: test api-lint deployment-check

api-lint:
	npx --yes @redocly/cli@2.40.0 lint internal/httpapi/openapi.yaml

openapi-permissions:
	go run ./cmd/withhuman-openapi-permissions

deployment-check:
	sh -n scripts/third-party-notices.sh scripts/check-cli-builds.sh deployment/cli/install.sh scripts/smoke.sh scripts/smoke-onboarding.sh scripts/smoke-rbac.sh

build: frontend
	go build ./cmd/withhuman ./cmd/withhuman-cli

# The smoke scripts run against any withHuman server that still offers
# the bootstrap ceremony, so a single binary started with
# WITHHUMAN_BOOTSTRAP_SECRET=change-me-before-production is enough.
smoke:
	./scripts/smoke.sh

smoke-onboarding:
	./scripts/smoke-onboarding.sh

smoke-rbac:
	./scripts/smoke-rbac.sh
