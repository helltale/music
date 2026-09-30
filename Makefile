COMPOSE := docker compose --project-directory . -f deploy/compose/docker-compose.yml

.PHONY: config build up down ps logs test vet restart

config:
	$(COMPOSE) config

build:
	$(COMPOSE) build

up: .env
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

ps:
	$(COMPOSE) ps

logs:
	$(COMPOSE) logs

restart:
	$(COMPOSE) restart music-api music-worker music-web

test:
	docker run --rm -v "$(CURDIR)/backend:/src" -w /src golang:1.27.1 go test ./...

vet:
	docker run --rm -v "$(CURDIR)/backend:/src" -w /src golang:1.27.1 go vet ./...

.env:
	cp .env.example .env
