.PHONY: dev build web clean

# Build everything
build: web
	go build -tags "sqlite_fts5" -o nexus ./cmd/nexus/

# Build SvelteKit
web:
	npm --prefix web run build

# Dev server (build + run)
dev: build
	./nexus serve --dev

# Clean
clean:
	rm -f nexus
	rm -rf web/build web/.svelte-kit
	rm -rf ~/.nexus
