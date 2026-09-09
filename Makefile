.PHONY: test test-pr test-production test-fault test-upgrade test-nightly test-chaos test-soak test-soak-constrained

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
