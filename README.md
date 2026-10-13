# Music

Собственный музыкальный стриминговый сервис: свой каталог, своё хранилище аудио и свой API. Внешние сервисы подключаются как providers метаданных и разрешённого аудио. Во время обычного просмотра и playback источник истины — своя база и свой Object Storage.

Сейчас реализованы Phase 1–13: контейнеры, каталог, тестовый провайдер, очередь import_jobs, импорт артиста, подпись playback URL, web UI, каталог MusicBrainz, легальная бесплатная загрузка аудио по ссылке MusicBrainz, ручная и автоматическая синхронизация уже импортированного артиста. Обычные коммерческие записи без такой ссылки по-прежнему нечем воспроизвести. Автосинхронизация выключена, пока её не включат у конкретного артиста.

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


!! поправить модуль скачивания треков через yt-dlp
https://github.com/yt-dlp/yt-dlp
