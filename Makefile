GREEN=\033[0;32m
CYAN=\033[0;36m
NC=\033[0m # No Color

# Sprawdzenie czy skrypt jest uruchomiony z uprawnieniami roota
USERID := $(shell id -u)
IS_ROOT := $(shell if [ $(USERID) = "0" ]; then echo "true"; else echo "false"; fi)

.PHONY: all tidy

all: tidy
	@echo "$(CYAN)Building gNb bot...$(NC)"
	@CGO_ENABLED=0 go build -ldflags="-s -w -extldflags=-static" -o gNb cmd/main.go
	@echo "$(GREEN)Build completed successfully! Binary: gNb$(NC)"
ifeq ($(IS_ROOT),true)
	@echo "$(CYAN)Installing binary to /bin/gNb...$(NC)"
	@cp gNb /bin/gNb
	@chmod +x /bin/gNb
	@echo "$(GREEN)Binary installed successfully to /bin/gNb$(NC)"
endif

tidy:
	@echo "$(CYAN)Syncing Go modules...$(NC)"
	@go mod tidy
	@echo "$(GREEN)Modules synced successfully!$(NC)"
