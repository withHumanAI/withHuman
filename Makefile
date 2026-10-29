.PHONY: all frontend fmt test hermes-plugin-test openclaw-plugin-test openai-plugin runtime-plugin-test verify api-lint openapi-permissions deployment-check build smoke smoke-onboarding smoke-rbac

all: test build

frontend:
	cd frontend && npm ci && npm run build

fmt:
	gofmt -w cmd internal db domain application approvalblock authn entitlements loggermdc runtime singlebinary plugins

test: frontend hermes-plugin-test openclaw-plugin-test runtime-plugin-test
	go test $(GOTESTFLAGS) ./...
	go vet ./...
	cd frontend && npm test

# The Hermes plugin's tests; the ones against a real Hermes run only when
# Hermes is importable.
hermes-plugin-test:
	cd plugins/hermes/tests && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest

# The OpenClaw plugin's tests, against a fake withhuman CLI and server.
openclaw-plugin-test:
	node --test plugins/openclaw/tests/*.test.mjs

# The OpenAI plugin carries copies of the published skills, since a plugin
# cannot link outside its folder. This refreshes them and builds the ZIP
# the submission portal takes.
openai-plugin:
	rm -rf plugins/openai/withhuman/skills
	mkdir -p plugins/openai/withhuman/skills dist
	cp -R skills/withhuman skills/withhuman-pipelines plugins/openai/withhuman/skills/
	rm -f dist/withhuman-openai-plugin.zip
	cd plugins/openai/withhuman && zip -qrX ../../../dist/withhuman-openai-plugin.zip . -x '.*'

# The launchers the Gemini CLI extension and the Antigravity plugin at the
# repository root run.
runtime-plugin-test:
	node --test plugins/gemini/launcher.test.mjs plugins/antigravity/launcher.test.mjs

verify: test api-lint deployment-check

api-lint:
	npx --yes @redocly/cli@2.40.0 lint internal/httpapi/openapi.yaml

openapi-permissions:
	go run ./cmd/withhuman-openapi-permissions

deployment-check:
	sh -n scripts/third-party-notices.sh scripts/check-cli-builds.sh deployment/cli/install.sh plugins/antigravity/launcher.sh scripts/smoke.sh scripts/smoke-onboarding.sh scripts/smoke-rbac.sh

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
