# Zero-Trust API Gateway demo — convenience targets.
# Deployment itself is done step-by-step from the README (kind/kubectl/helm).
# These targets just wrap the pre-pull and the live-demo helpers.

.PHONY: help prepull token call load logs teardown

help:
	@echo "Targets:"
	@echo "  make prepull               Pull + cache all workload images (run before demo)"
	@echo "  make token U=bob           Print an access token for a user"
	@echo "  make call TIER=pro U=bob   Call a tier endpoint (use U=--anon for no token)"
	@echo "  make load TIER=free        Trip the rate limiter"
	@echo "  make logs                  Tail Traefik JSON access logs"
	@echo "  make teardown              Delete the cluster"
	@echo ""
	@echo "Deploy steps live in README.md (kind create cluster + helm + kubectl apply)."

prepull:  ; ./scripts/00-prepull-images.sh

token: ; ./scripts/get-token.sh $(or $(U),alice)
call:  ; ./scripts/call-api.sh $(or $(TIER),free) $(or $(U),alice)
load:  ; ./scripts/loadtest.sh $(or $(TIER),free) $(or $(U),alice)

logs:    ; kubectl logs -n traefik -l app.kubernetes.io/name=traefik -f
teardown:; ./scripts/99-teardown.sh
