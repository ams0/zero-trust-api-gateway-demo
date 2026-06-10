# apikey-operator — build & deploy helpers.
IMG ?= apikey-operator:demo
CLUSTER ?= zero-trust-gw
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@latest
ARCH := linux/$(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/;s/arm64/arm64/')

.PHONY: help generate manifests fmt vet build test docker-build kind-load install uninstall deploy undeploy gateway

help:
	@echo "Targets: generate manifests build test docker-build kind-load install deploy gateway undeploy"

generate: ## deepcopy
	$(CONTROLLER_GEN) object paths=./api/...

manifests: ## CRD + RBAC
	$(CONTROLLER_GEN) crd rbac:roleName=apikey-manager-role paths=./... \
	  output:crd:dir=config/crd/bases output:rbac:dir=config/rbac

fmt: ; go fmt ./...
vet: ; go vet ./...
build: generate fmt vet ; go build -o bin/manager ./cmd
test: generate fmt vet ; go test ./... -count=1

docker-build: ## build the manager image for the cluster arch
	docker build --platform $(ARCH) -t $(IMG) .

kind-load: docker-build ## side-load into kind (containerd-store-safe path)
	docker image save --platform $(ARCH) $(IMG) -o /tmp/apikey-operator.tar
	kind load image-archive /tmp/apikey-operator.tar --name $(CLUSTER)
	rm -f /tmp/apikey-operator.tar

install: ## install the CRD
	kubectl apply -f config/crd/bases

uninstall:
	kubectl delete -f config/crd/bases --ignore-not-found

deploy: install ## RBAC + manager
	kubectl apply -f config/rbac/role.yaml
	kubectl apply -f config/manager/manager.yaml
	kubectl apply -f config/rbac/service_account.yaml
	kubectl apply -f config/rbac/role_binding.yaml

gateway: ## dual-auth (X-API-Key) routes on the gateway
	kubectl apply -f deploy/gateway/apikey-routes.yaml

undeploy:
	kubectl delete -f deploy/gateway/apikey-routes.yaml --ignore-not-found
	kubectl delete -f config/manager/manager.yaml --ignore-not-found
	kubectl delete -f config/rbac/role_binding.yaml --ignore-not-found
	kubectl delete -f config/rbac/service_account.yaml --ignore-not-found
	kubectl delete -f config/rbac/role.yaml --ignore-not-found
