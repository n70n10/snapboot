BINARY  := snapboot
PREFIX  := /usr/local
DESTDIR :=

build:
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/snapboot

install: build
	install -Dm755 $(BINARY) $(DESTDIR)$(PREFIX)/bin/$(BINARY)
	$(DESTDIR)$(PREFIX)/bin/$(BINARY) install

uninstall:
	$(DESTDIR)$(PREFIX)/bin/$(BINARY) uninstall
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)

# Convenience: rebuild and re-sync after editing
dev: build
	sudo ./$(BINARY) sync

.PHONY: build install uninstall clean dev
