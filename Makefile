.PHONY: test test-pr test-production test-fault test-upgrade test-nightly test-chaos test-soak test-soak-constrained test-admin-ui

test:
	go test ./...

test-pr:
	./scripts/test-production.sh pr

test-production:
	./scripts/test-production.sh production

test-fault:
	./scripts/test-production.sh fault

test-upgrade:
	./scripts/test-production.sh upgrade

test-nightly:
	./scripts/test-production.sh nightly

test-chaos:
	./scripts/test-production.sh chaos

test-soak:
	./scripts/run-btree-soak.sh

test-soak-constrained:
	./scripts/run-btree-soak-constrained.sh

# NextSQL Admin's web UI. Needs Node and a Chrome/Chromium (set CHROME_BIN if
# it is not at one of the usual paths) — which is why it is not yet part of
# test-pr/test-production; see docs/production/GAPS.md. Runnable by name so it
# stops being invisible: it had been failing continuously and no gate said so.
test-admin-ui:
	cd internal/admin/frontend && npm run typecheck && npm run test:studio \
	  && npm run test:setup && npm run test:revision && npm run test:a11y
