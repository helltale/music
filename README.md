# Music

Собственный музыкальный стриминговый сервис: свой каталог, своё хранилище аудио и свой API. Внешние сервисы подключаются как providers метаданных и разрешённого аудио. Во время обычного просмотра и playback источник истины — своя база и свой Object Storage.

Сейчас собран каркас Phase 1: API, worker, migrate, web, PostgreSQL и MinIO запускаются контейнерами. Каталога и playback ещё нет.

- [Архитектура](docs/architecture.md)
- [Модель данных](docs/data-model.md)
- [HTTP API](docs/api.md)
- [Import pipeline](docs/import-pipeline.md)
- [Deployment](docs/deployment.md)
- [Master prompt](docs/master_prompt.md)

Локальный запуск из корня репозитория:

```bash
cp .env.example .env
docker compose up -d
```

Web UI: <http://localhost:3000>. Пробы API: <http://localhost:8080/health> и <http://localhost:8080/ready>.
