BINARY := moon-shell
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
SYSCONFDIR ?= /etc
SYSTEMD_DIR ?= $(SYSCONFDIR)/systemd/system
CONFIG_DIR ?= $(SYSCONFDIR)/moon-shell
STATE_DIR ?= /var/lib/moon-shell
SERVICE_USER ?= moon-shell
SERVICE_GROUP ?= moon-shell
SERVICE_NAME ?= moon-shell.service
SERVICE_SRC := contrib/systemd/$(SERVICE_NAME)
BUILD_DIR ?= bin
GO ?= go
INSTALL ?= install
SYSTEMCTL ?= systemctl
USERADD ?= useradd

.PHONY: all build test install install-bin install-user install-config install-state install-systemd uninstall-systemd uninstall clean

all: build

build:
	$(GO) build -o $(BUILD_DIR)/$(BINARY) .

test:
	$(GO) test ./...

install: install-bin install-user install-config install-state install-systemd

install-bin: build
	$(INSTALL) -d $(DESTDIR)$(BINDIR)
	$(INSTALL) -m 0755 $(BUILD_DIR)/$(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

install-user:
	@if [ -n "$(DESTDIR)" ]; then \
		echo "Skipping service user creation for DESTDIR install"; \
	elif id -u "$(SERVICE_USER)" >/dev/null 2>&1; then \
		echo "Service user $(SERVICE_USER) already exists"; \
	else \
		$(USERADD) --system --home-dir "$(STATE_DIR)" --create-home --shell /usr/sbin/nologin "$(SERVICE_USER)"; \
		echo "Created service user $(SERVICE_USER)"; \
	fi

install-config:
	$(INSTALL) -d -m 0755 $(DESTDIR)$(CONFIG_DIR)
	@if [ ! -f "$(DESTDIR)$(CONFIG_DIR)/config.yml" ]; then \
		$(INSTALL) -m 0640 config.example.yml "$(DESTDIR)$(CONFIG_DIR)/config.yml"; \
		sed -i 's|^execution_db:.*|execution_db: /var/lib/moon-shell/moon-shell.exec.db|' "$(DESTDIR)$(CONFIG_DIR)/config.yml"; \
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
	@if [ -z "$(DESTDIR)" ] && id -u "$(SERVICE_USER)" >/dev/null 2>&1; then \
		chown -R root:$(SERVICE_GROUP) "$(CONFIG_DIR)"; \
		chmod 0750 "$(CONFIG_DIR)"; \
	fi

install-state:
	$(INSTALL) -d -m 0750 $(DESTDIR)$(STATE_DIR)
	@if [ -z "$(DESTDIR)" ] && id -u "$(SERVICE_USER)" >/dev/null 2>&1; then \
		chown "$(SERVICE_USER):$(SERVICE_GROUP)" "$(STATE_DIR)"; \
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
