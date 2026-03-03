.PHONY: dev build web clean

# Build everything
build: web
	go build -o nexus ./cmd/nexus/

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
