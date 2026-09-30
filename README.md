# Music

Собственный музыкальный стриминговый сервис: свой каталог, своё хранилище аудио и свой API. Внешние сервисы подключаются как providers метаданных и разрешённого аудио. Во время обычного просмотра и playback источник истины — своя база и свой Object Storage.

Сейчас реализованы Phase 1–6: контейнеры, каталог, тестовый провайдер, очередь import_jobs и обработка одного аудиофайла до READY в MinIO. Worker импортирует каталог и пока не запускает audio. Playback ещё нет.

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
