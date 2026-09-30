# HTTP API

Базовый префикс: `/api/v1`. Кодировка: `application/json; charset=utf-8`.

Пробы живут вне версии API: `GET /health`, `GET /ready`.

Браузер вызывает API на том же origin, что и UI. `music-web` проксирует `/api/v1` на `music-api`. Прямой адрес API нужен для отладки и для worker-тестов, контракт от этого не меняется.

Лимит тела запроса — 1 МиБ. Таймаут запроса API — 15 секунд. Импорт в этот бюджет укладывается, потому что запрос только создаёт job.

Аутентификации на MVP нет. Это допустимо в локальном Compose и недопустимо на публичном stage.

## Ошибки

Тело ошибки:

```json
{
  "error": {
    "code": "RECORDING_NOT_FOUND",
    "message": "Recording not found"
  }
}
```

`message` — короткая английская фраза для клиента и логов поддержки. Внутренний текст драйвера, SQL и стек в ответ не попадают. Они остаются в логе вместе с `request_id`.

Заголовок `X-Request-Id` возвращается всегда. Если клиент прислал свой, API использует его, иначе генерирует UUID.

| HTTP | code | Когда |
| --- | --- | --- |
| 400 | `VALIDATION_ERROR` | Неверный UUID, пустой `q`, неизвестное поле, слишком большой `limit` |
| 404 | `NOT_FOUND` | Нет такого пути |
| 404 | `ARTIST_NOT_FOUND` | Нет артиста |
| 404 | `RELEASE_NOT_FOUND` | Нет релиза |
| 404 | `RECORDING_NOT_FOUND` | Нет записи |
| 404 | `IMPORT_JOB_NOT_FOUND` | Нет job |
| 409 | `AUDIO_NOT_READY` | Запись есть, READY asset нет |
| 409 | `JOB_NOT_RETRYABLE` | Retry у job в `QUEUED`, `RUNNING` или `COMPLETED` |
| 500 | `INTERNAL` | Непредвиденная ошибка. Сообщение общее |

Успешные коды: `200` для чтения и повторного попадания в уже активный импорт, `201` для нового job.

## Пагинация

Списки используют `limit` и `offset`.

- default `limit` = 20
- max `limit` = 100
- default `offset` = 0

Ответ:

```json
{
  "items": [],
  "limit": 20,
  "offset": 0,
  "total": 0
}
```

`total` — полное число строк под тем же фильтром. На размере MVP это дешёвый `COUNT`. Курсоры не вводятся.

## Пробы

`GET /health` → `200 {"status":"ok"}`, если процесс жив. Базу не трогает.

`GET /ready` → `200 {"status":"ready"}`, если процесс не останавливается и `Ping` PostgreSQL уложился в короткий таймаут. Иначе `503 {"status":"not_ready"}`.

Те же пути есть у worker на внутреннем порту. В пробах нет тяжёлых запросов и нет похода в MinIO.

## Публичный каталог

Идентификаторы в пути — UUID. Не-UUID даёт `400 VALIDATION_ERROR`.

### GET /api/v1/artists

Локальный каталог, не provider.

Query: `q`, `limit`, `offset`. `q` необязателен. Пустой `q` возвращает артистов по имени. Непустой `q` — `name ILIKE '%' || q || '%'`.

Элемент `items`:

```json
{
  "id": "uuid",
  "name": "Test Artist"
}
```

### GET /api/v1/artists/{id}

```json
{
  "id": "uuid",
  "name": "Test Artist",
  "description": null,
  "image_object_key": null
}
```

Релизы этим ответом не подмешиваются.

### GET /api/v1/artists/{id}/releases

Релизы, где артист есть в `release_artists`. Сортировка: дата убывает, NULL в конце, затем title.

```json
{
  "items": [
    {
      "id": "uuid",
      "title": "First Album",
      "release_type": "album",
      "release_date": "2024-01-15",
      "cover_object_key": null
    }
  ],
  "limit": 20,
  "offset": 0,
  "total": 1
}
```

`release_date` — `YYYY-MM-DD` или `null`.

### GET /api/v1/releases/{id}

```json
{
  "id": "uuid",
  "title": "First Album",
  "release_type": "album",
  "release_date": "2024-01-15",
  "cover_object_key": null,
  "artists": [
    {"id": "uuid", "name": "Test Artist", "role": "primary"}
  ],
  "tracks": [
    {
      "disc_number": 1,
      "track_number": 1,
      "title": "Track One",
      "recording_id": "uuid",
      "duration_ms": 180000,
      "playback_available": false
    }
  ]
}
```

`title` трека — `title_override`, если он задан, иначе title Recording. `duration_ms` — каталожная длительность. `playback_available` вычисляется в составе HTTP-ответа: существует asset этой Recording в `READY` с непустым `playable_object_key`. В таблицу `recordings` это не записывается.

Список треков в этом ответе полный, без пагинации. Релиз MVP умещается в один ответ. Если позже появятся релизы на сотни позиций, пагинацию добавят отдельно.

### GET /api/v1/recordings/{id}

```json
{
  "id": "uuid",
  "title": "Track One",
  "isrc": "USTST2600001",
  "duration_ms": 180000,
  "artists": [
    {"id": "uuid", "name": "Test Artist", "role": "primary"}
  ],
  "playback_available": true
}
```

## Playback

### GET /api/v1/recordings/{id}/play

1. Прочитать Recording. Нет строки → `404 RECORDING_NOT_FOUND`.
2. Найти её `audio_assets` со статусом `READY` и непустым `playable_object_key`.
3. Нет такого asset → `409 AUDIO_NOT_READY`. Это же ответ для `PENDING`, `ACQUIRING`, `PROCESSING`, `FAILED` и для отсутствующей строки.
4. Подписать GET на `S3_PUBLIC_ENDPOINT` со сроком `PLAYBACK_URL_TTL` (по умолчанию 1 час).
5. Вернуть метаданные, без ключа бакета и без секретов.

```json
{
  "recording_id": "uuid",
  "audio_asset_id": "uuid",
  "url": "http://localhost:9000/music/recordings/...",
  "expires_at": "2026-09-30T19:00:00Z",
  "content_type": "audio/mp4",
  "duration_ms": 180000
}
```

`duration_ms` здесь — длительность playable из asset. `content_type` всегда `audio/mp4`.

Браузер кладёт `url` в `<audio src>`. Атрибут `crossorigin` не ставится: обычное воспроизведение кросс-доменного файла им не пользуется. Range-запросы отдаёт Object Storage.

API эти байты не читает и не проксирует. Полный подписанный URL в лог не пишется. В логе остаются `request_id`, `recording_id`, `audio_asset_id`.

Срок ссылки больше любой демо-записи. Обновление URL посреди длинного трека на MVP не делается. Истёкшая ссылка при seek — известное ограничение, пока трек короче TTL.

## Admin

Поиск кандидатов идёт в CatalogProvider. Публичный поиск артистов идёт в PostgreSQL. Это разные операции.

### GET /api/v1/admin/catalog/artists/search

Query: `q` обязателен, после trim не пустой. Иначе `400`. `limit` как у остальных списков, `offset` provider на MVP может игнорировать: фейковый каталог возвращает всех совпавших и `total`.

Совпадение — подстрока имени без учёта регистра, на стороне provider.

```json
{
  "items": [
    {
      "provider": "fake",
      "external_id": "test-artist",
      "name": "Test Artist"
    }
  ],
  "limit": 20,
  "offset": 0,
  "total": 1
}
```

В ответе нет внутренних UUID: артиста в нашей базе ещё может не быть.

### POST /api/v1/admin/catalog/artists/import

```json
{
  "provider": "fake",
  "external_id": "test-artist"
}
```

Оба поля обязательны и не пустые. Неизвестный provider → `400 VALIDATION_ERROR`.

Поведение:

- нет активного job этой пары `(artist_import, provider, external_id)` → создать job `QUEUED`, ответ `201`;
- активный job уже есть → `200` и этот job, новый не создавать.

Тело в обоих случаях — объект job (схема ниже). HTTP-запрос не ждёт каталог и audio.

Неизвестный `external_id` на этом шаге не проверяется у provider. Ошибку «артист не найден» получит worker, job станет `FAILED` без повторов.

### GET /api/v1/admin/import-jobs

Query: `limit`, `offset`, необязательный `status`. Неизвестный `status` → `400`. Сортировка: `created_at DESC`.

### GET /api/v1/admin/import-jobs/{id}

Один объект job.

```json
{
  "id": "uuid",
  "type": "artist_import",
  "status": "PARTIALLY_COMPLETED",
  "provider": "fake",
  "external_id": "test-artist",
  "attempt": 1,
  "max_attempts": 5,
  "progress": {
    "artist_id": "uuid",
    "releases_total": 2,
    "releases_done": 2,
    "recordings_total": 3,
    "recordings_imported": 3,
    "audio_ready": 2,
    "audio_failed": 1
  },
  "last_error": null,
  "created_at": "2026-09-30T18:00:00Z",
  "started_at": "2026-09-30T18:00:02Z",
  "finished_at": "2026-09-30T18:00:05Z"
}
```

В списке элемент тот же. `payload` наружу не отдаётся.

### POST /api/v1/admin/import-jobs/{id}/retry

Без тела.

- `FAILED` или `PARTIALLY_COMPLETED` → статус `QUEUED`, `attempt = 0`, lock снят, `last_error` очищен, `finished_at` очищен, `next_attempt_at = now()`. Ответ `200` и job.
- `QUEUED`, `RUNNING`, `COMPLETED` → `409 JOB_NOT_RETRYABLE`.

Повторный импорт уже успешного артиста — новый `POST .../artists/import`, не retry. Retry снова обрабатывает тот же job и проходит pipeline идемпотентно: готовые asset с тем же checksum пропускаются, `FAILED` asset обрабатываются заново.

## Что API не делает на MVP

- не отдаёт аудио байтами;
- не ходит в provider при открытии артиста, релиза и записи;
- не принимает загрузку пользовательских файлов;
- не удаляет сущности;
- не синхронизирует артиста отдельной командой (`POST /admin/artists/{id}/sync` — Phase 12).
