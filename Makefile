BINARY := moon-shell
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
SYSCONFDIR ?= /etc
SYSTEMD_DIR ?= $(SYSCONFDIR)/systemd/system
CONFIG_DIR ?= $(SYSCONFDIR)/moon-shell
SERVICE_NAME ?= moon-shell.service
SERVICE_SRC := contrib/systemd/$(SERVICE_NAME)
BUILD_DIR ?= bin
GO ?= go
INSTALL ?= install
SYSTEMCTL ?= systemctl

.PHONY: all build test install install-bin install-config install-systemd uninstall-systemd uninstall clean

all: build

build:
	$(GO) build -o $(BUILD_DIR)/$(BINARY) .

test:
	$(GO) test ./...

install: install-bin install-config install-systemd

install-bin: build
	$(INSTALL) -d $(DESTDIR)$(BINDIR)
	$(INSTALL) -m 0755 $(BUILD_DIR)/$(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

install-config:
	$(INSTALL) -d -m 0755 $(DESTDIR)$(CONFIG_DIR)
	@if [ ! -f "$(DESTDIR)$(CONFIG_DIR)/config.yml" ]; then \
		$(INSTALL) -m 0640 config.example.yml "$(DESTDIR)$(CONFIG_DIR)/config.yml"; \
		echo "Installed example config to $(DESTDIR)$(CONFIG_DIR)/config.yml"; \
	else \
		echo "Keeping existing $(DESTDIR)$(CONFIG_DIR)/config.yml"; \
	fi
	@if [ ! -f "$(DESTDIR)$(CONFIG_DIR)/moon-shell.env" ]; then \
		: > "$(DESTDIR)$(CONFIG_DIR)/moon-shell.env"; \
		chmod 0640 "$(DESTDIR)$(CONFIG_DIR)/moon-shell.env"; \
		echo "Created empty $(DESTDIR)$(CONFIG_DIR)/moon-shell.env"; \
	else \
		echo "Keeping existing $(DESTDIR)$(CONFIG_DIR)/moon-shell.env"; \
	fi

install-systemd:
	$(INSTALL) -d $(DESTDIR)$(SYSTEMD_DIR)
	$(INSTALL) -m 0644 $(SERVICE_SRC) $(DESTDIR)$(SYSTEMD_DIR)/$(SERVICE_NAME)
	@if command -v $(SYSTEMCTL) >/dev/null 2>&1 && [ -z "$(DESTDIR)" ]; then \
		$(SYSTEMCTL) daemon-reload; \
		echo "Installed $(SERVICE_NAME). Enable with: $(SYSTEMCTL) enable --now $(SERVICE_NAME)"; \
	fi

uninstall-systemd:
	@if command -v $(SYSTEMCTL) >/dev/null 2>&1 && [ -z "$(DESTDIR)" ]; then \
		-$(SYSTEMCTL) disable --now $(SERVICE_NAME); \
	fi
	rm -f $(DESTDIR)$(SYSTEMD_DIR)/$(SERVICE_NAME)
	@if command -v $(SYSTEMCTL) >/dev/null 2>&1 && [ -z "$(DESTDIR)" ]; then \
		$(SYSTEMCTL) daemon-reload; \
	fi

uninstall: uninstall-systemd
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)

clean:
	rm -rf $(BUILD_DIR)
