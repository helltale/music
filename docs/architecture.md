# Архитектура

Статус: Phase 4 — FakeCatalogProvider и поиск артиста для админа. Очередь задач, audio и playback ещё не реализованы. Этот документ — принятая архитектура MVP. Процесс разработки остаётся в [master_prompt.md](master_prompt.md).

Связанные документы:

- [Модель данных](data-model.md)
- [HTTP API](api.md)
- [Import pipeline](import-pipeline.md)
- [Deployment](deployment.md)

Если этот документ расходится с master prompt, для реализации действует этот документ: расхождения перечислены явно. Правило внешнего ключа `audio_assets.recording_id` с master prompt совпадает: FK между модулями нет.

## Что это за система

Собственный музыкальный каталог и собственное хранилище playable audio. Внешний provider нужен для discovery, первичного импорта, последующей синхронизации метаданных и получения разрешённого audio. Страницы Artist, Release и Recording читают PostgreSQL. Playback читает подписанный URL и забирает байты из Object Storage.

Источник истины каталога — PostgreSQL. Источник истины playable audio — Object Storage. Строка `audio_assets` хранит ключ объекта и техническое состояние файла.

## Границы системы

```text
Browser
  |
  | UI и /api/v1 (same origin)
  v
music-web
  |
  | HTTP, внутренняя сеть
  v
music-api
  |
  +---- PostgreSQL
  |
  +---- Object Storage (только подпись URL)

music-worker
  |
  +---- PostgreSQL (claim jobs, catalog, audio_assets)
  +---- CatalogProvider
  +---- AudioProvider
  +---- FFmpeg / ffprobe
  +---- Object Storage (Put)

Browser -------- GET подписанного URL --------> Object Storage
```

API не передаёт аудиопоток. Worker не обслуживает пользовательский HTTP, кроме `/health` и `/ready`.

## Модули

Один Go-модуль, два долгоживущих процесса плюс одноразовая команда миграций.

| Модуль | Ответственность |
| --- | --- |
| Catalog | Artist, Release, Recording, ReleaseTrack, связи артистов, external IDs, Identity Resolver, запись каталога |
| Audio | AudioAsset, AudioProvider, validation, ffprobe, FFmpeg, Object Storage |
| Import | ImportJob, claim, lease, retry, recovery, оркестрация импорта |
| Streaming | Поиск READY-ассета и выдача короткой подписанной ссылки |

`cmd/api`, `cmd/worker` и `cmd/migrate` только собирают зависимости и запускают процесс. Бизнес-правила туда не класть.

Общий технический код без доменных правил — `internal/platform`: конфигурация, JSON-логи, пул PostgreSQL, HTTP-сервер, graceful shutdown. Это замена предложенного `internal/infrastructure`: так меньше шансов, что в одном пакете окажутся и MinIO, и каталог.

Зависимости направлены внутрь:

```text
cmd/api      -> catalog, importjob, streaming, platform
cmd/worker   -> importjob, platform
cmd/migrate  -> platform

importjob    -> catalog, audio, platform
streaming    -> catalog, audio, platform
catalog      -> platform
audio        -> platform
```

Catalog не импортирует Audio. Audio не импортирует Catalog: `recording_id` для Audio — обычный UUID. Проверка «запись только что создана импортом» стоит в оркестраторе Import, который уже выполнил запись каталога.

Streaming — тонкий сценарий playback. Он читает Catalog и Audio и подписывает URL. Он не качает файл и не пишет каталог.

На старте пакеты плоские: `internal/catalog`, `internal/audio`, `internal/importjob`, `internal/streaming`. Вложенные `domain/application/infrastructure` не создавать, пока в пакете не станет тесно.

Планируемый путь модуля: `github.com/helltale/music/backend`.

Стек, зафиксированный на Phase 0:

- Go 1.27.1
- PostgreSQL 18
- Next.js 16.3 (Active LTS), точный patch pin — в Phase 1, на актуальном security release
- MinIO для локального S3
- FFmpeg и ffprobe только в образе worker

## Runtime

| Процесс | Роль |
| --- | --- |
| music-api | REST, подпись playback URL, постановка ImportJob. Stateless |
| music-worker | Claim jobs, каталог, audio pipeline. Stateless, кроме `/tmp` на время одного job |
| music-web | Next.js standalone. Отдаёт UI и проксирует `/api/v1` на API |
| migrate | Один прогон схемы и выход. Не sidecar API |
| postgres | Единственная transactional БД |
| minio | Локальный Object Storage |

Несколько реплик API и несколько реплик Worker — нормальный режим. В коде нет режима «процесс один». Реплики не хранят общее состояние в памяти и не делят файловую систему контейнера.

Локально Docker Compose запускает одну реплику каждого процесса. Вторая реплика Worker поднимается без фиксированного `container_name` и без проброса порта worker на хост.

## Catalog и Audio

Recording существует без playable-файла. У Recording нет статусов `AUDIO_PROCESSING`, `AUDIO_READY`, `AUDIO_FAILED`.

Доступность playback вычисляется при чтении: у Recording есть `audio_assets.status = READY` и непустой `playable_object_key`. Это поле ответа API, не колонка каталога.

Между модулями допустима отложенная согласованность: Recording уже в каталоге, AudioAsset ещё `PROCESSING` или `FAILED`. Ошибка audio не откатывает каталог.

Внутри Catalog короткая транзакция покрывает один агрегат целиком. Eventual consistency внутри создания Release не используется.

### Ссылка Audio → Catalog

`audio_assets.recording_id` — UUID без FK на `recordings.id`. Так решено для MVP: внешний ключ остаётся внутри одного модуля, ссылка между Audio и Catalog проверяется приложением.

Оркестратор Import создаёт asset только для Recording, которую сам только что записал или нашёл. Удаления Recording в API нет. Уникальность одной строки asset на запись даёт `UNIQUE (recording_id)`. Отдельный индекс не нужен: его покрывает этот ключ.

## Import

Очередь — таблица `import_jobs` в PostgreSQL. NATS нет.

На MVP один тип job: `artist_import`. Worker в этом job сначала пишет каталог, затем для каждой Recording запускает audio pipeline. Отдельная очередь и отдельные audio-jobs не вводятся, пока один импорт артиста на практике не станет слишком долгим для одного worker или не понадобится параллельная обработка записей.

Идемпотентность держится на external ID, Identity Resolver и уникальности `audio_assets.recording_id`, а не на «job выполняется один раз в жизни». Повтор после падения worker безопасен.

Подробности claim, lease, fencing и частичного сбоя — в [import-pipeline.md](import-pipeline.md).

## Streaming

`GET /api/v1/recordings/{id}/play` находит READY asset и возвращает presigned GET. Браузер открывает URL напрямую.

Подпись строится на публичный endpoint, который видит браузер. Загрузка файла worker-ом идёт на внутренний endpoint. Это два разных адреса одного бакета. Иначе ссылка содержит `minio:9000` и в браузере не открывается.

## Контейнеры

С первого дня рабочий запуск — `docker compose up -d`. На хосте не требуются Go, Node.js, PostgreSQL, MinIO и FFmpeg.

Образы:

- `music-api` — API и бинарь `migrate`. Без FFmpeg, без исходников, без `.env`
- `music-worker` — worker и FFmpeg/ffprobe. В образ копируется только короткий демо-набор audio для `LocalAudioProvider`, не тесты
- `music-web` — `next build` и `output: "standalone"`. Не `next dev`

Production-процессы запускаются не от root. Локальный тег сборки `dev` остаётся на машине разработчика. Stage и production получают неизменяемый тег вида `git-<sha>` или версию. Один и тот же образ проходит test → stage → production. Отличия среды — переменные окружения и секреты.

`NEXT_PUBLIC_*` не используется: Next.js вшивает такие переменные на этапе сборки и ломает правило «один образ». Браузер ходит на same origin в `music-web`, а сервер Next.js проксирует `/api/v1` на `API_UPSTREAM`. Прокси читает `API_UPSTREAM` на каждом запросе. `rewrites()` в `next.config` для этого не подходят: адрес upstream там фиксируется при сборке образа.

## Решения Phase 0

### 1. Playable format

Один файл: контейнер MP4, кодек AAC-LC, `audio/mp4`, расширение `.m4a`, битрейт 192 кбит/с, каналы доведены до стерео.

Почему так:

- элемент `<audio>` воспроизводит это в текущих Chrome, Firefox, Safari и Edge, включая мобильный Safari;
- один файл, без плейлиста HLS/DASH;
- seek работает через HTTP Range, если `moov` в начале файла;
- в FFmpeg достаточно встроенного энкодера `aac`, отдельный `libfdk_aac` не нужен.

Команда транскодирования:

```text
ffmpeg -y -i <source> -vn -map 0:a:0 -c:a aac -b:a 192k -ac 2 -movflags +faststart <dest>.m4a
```

`-vn` отбрасывает обложку, вшитую как видео. `+faststart` переносит `moov` в начало после кодирования, чтобы браузер начал playback и seek без скачивания всего файла. Исходник сначала проверяется `ffprobe`.

Технические поля asset описывают playable-файл. SHA-256 считается по original bytes.

### 2. Original audio

Original хранится вместе с playable.

Повторное транскодирование не зависит от повторного скачивания у provider. Отладка битого playable остаётся возможной. Колонка `original_object_key` nullable: позже можно перестать писать original, не меняя модель.

Удаление original после успешного транскода на MVP не делается.

### 3. Claim job

Короткий один statement, без длинной транзакции на время импорта:

```sql
UPDATE import_jobs
SET status = 'RUNNING',
    locked_at = now(),
    locked_by = $worker_id,
    started_at = now(),
    attempt = attempt + 1
WHERE id = (
    SELECT id
    FROM import_jobs
    WHERE status = 'QUEUED'
      AND next_attempt_at <= now()
      AND attempt < max_attempts
    ORDER BY next_attempt_at, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;
```

`FOR UPDATE SKIP LOCKED` позволяет нескольким worker забирать разные jobs. Сравнение времени только через `now()` PostgreSQL, не через часы контейнера.

`locked_by` — UUID процесса, сгенерированный при старте. Переменная `WORKER_INSTANCE_ID` может задать его в тесте. IP и hostname идентификатором не являются.

### 4. Lease

Lease — 2 минуты. Heartbeat каждые 30 секунд обновляет `locked_at` у своего job:

```sql
UPDATE import_jobs
SET locked_at = now()
WHERE id = $id
  AND locked_by = $worker_id
  AND attempt = $attempt
  AND status = 'RUNNING';
```

Если statement не обновил строку, worker потерял job и останавливает работу.

Отдельный таймаут `AUDIO_PROCESS_TIMEOUT` (10 минут на одну Recording) убивает зависший FFmpeg. Иначе heartbeat держал бы зависший процесс бесконечно.

### 5. Зависший job

Перед каждым claim worker возвращает просроченные jobs:

```sql
UPDATE import_jobs
SET status = CASE
        WHEN attempt >= max_attempts THEN 'FAILED'
        ELSE 'QUEUED'
    END,
    locked_at = NULL,
    locked_by = NULL,
    next_attempt_at = now(),
    last_error = COALESCE(last_error, 'lease expired'),
    finished_at = CASE
        WHEN attempt >= max_attempts THEN now()
        ELSE NULL
    END
WHERE status = 'RUNNING'
  AND locked_at < now() - interval '2 minutes';
```

Attempt уже увеличен в момент claim, повторно при recovery не растёт. Любой worker может выполнить recovery: условие в `WHERE` не отдаст чужой живой lease.

### 6. Границы транзакций каталога

Вызов provider выполняется вне транзакции БД.

Дальше отдельные короткие транзакции:

1. Artist и его `artist_external_ids`.
2. Каждая Recording: сама запись, `recording_artists`, `recording_external_ids`.
3. Каждый Release: сам релиз, `release_artists`, `release_tracks`, `release_external_ids`.

Порядок фиксированный: artist, затем recordings, затем releases. `release_tracks` ссылается на уже существующие строки, FK внутри Catalog это проверяют.

Падение посередине оставляет уже записанные агрегаты. Повтор job догоняет их через Identity Resolver, а не откатывает успешную часть.

### 7. Идемпотентность импорта

- У каждой импортированной сущности в той же транзакции пишется external ID.
- `UNIQUE (provider, external_id)` не даёт двум строкам получить один внешний идентификатор. Гонка двух worker заканчивается уникальным нарушением, после которого операция перечитывает существующую строку.
- Пока job в `QUEUED` или `RUNNING`, второй импорт того же artist не создаётся: возвращается уже активный job.
- Повтор после `COMPLETED` создаёт новый job и обновляет метаданные без новых сущностей.
- Большие сырые ответы provider не сохраняются.

### 8. Одна и та же Recording

Resolver живёт в Catalog. Handlers, репозитории и providers его не дублируют.

Порядок для Recording:

1. Точное совпадение `provider + external_id` — та же сущность.
2. ISRC, если после нормализации найден ровно один Recording. Совпадение привязывает новый external ID к нему.
3. Если ISRC найден у двух и более Recording — автоматического слияния нет, создаётся новая Recording. Совпавшие id пишутся в лог job.
4. Иначе нормализованные primary artist и title плюс длительность в допуске ±2000 мс, и кандидат ровно один, и ISRC не противоречит. Тогда это та же Recording.
5. Иначе создаётся новая.

Нормализация: Unicode NFC, trim, схлопывание пробелов, нижний регистр. ISRC хранится 12 символами, без дефисов, в верхнем регистре.

Artist и Release на MVP связываются только по `provider + external_id`. Слияние артистов по имени даёт ложные склейки. Админ выбирает кандидата из поиска provider, а не из похожей строки в нашей базе.

Контракт provider: идентификатор Recording стабилен и не зависит от Release. Трек на двух релизах приходит с одним `external_id`. Внутри одного ответа импорт сначала собирает Recording по этому id в памяти, потом пишет в БД.

`UNIQUE (isrc)` нет.

### 9. Имена объектов

Бакет задаётся `S3_BUCKET`. Ключи:

```text
recordings/{recording_id}/g{generation}/original{ext}
recordings/{recording_id}/g{generation}/playable.m4a
```

`generation` увеличивается в БД в начале новой попытки обработки. Попытки пишут в разные префиксы, поэтому опоздавший worker не затирает файл победителя. В строке asset остаётся ключ той попытки, которая успешно записала `READY` под своим `generation`.

Чужие префиксы не удаляются. Сбор сирот на MVP нет.

Повтор с тем же SHA-256 original и статусом `READY` не увеличивает generation и не загружает файл заново.

Загрузка — обычный Put. Клиенты бакета используют path-style (`S3_USE_PATH_STYLE=true`): так устроен локальный MinIO.

### 10. Повтор audio

На Recording приходится не больше одной строки `audio_assets`.

Состояния: `PENDING`, `ACQUIRING`, `PROCESSING`, `READY`, `FAILED`.

- `PENDING` — строка создана, файл ещё не запрашивался.
- `ACQUIRING` — читается источник.
- `PROCESSING` — validation, ffprobe, checksum, FFmpeg.
- `READY` — playable объект записан, ключ опубликован в строке.
- `FAILED` — последняя попытка не удалась, `last_error` заполнен. Предыдущий playable не стирается из бакета, но playback смотрит только на `READY`.

Финальная публикация — одна транзакция из двух условных обновлений: строка asset (`WHERE generation = $mine`) и строка job (`WHERE locked_by = $worker AND attempt = $attempt`). Ноль строк у любого из них откатывает транзакцию: lease уже у другого worker, статус `READY` не публикуется. Временные файлы удаляются в `defer` и в этом случае тоже.

Каталог при `FAILED` asset не удаляется. Job получает `PARTIALLY_COMPLETED`, если каталог записан, а хотя бы один asset не стал `READY`.

### 11. SIGTERM

Общий порядок для API и Worker: сигнал `SIGTERM` или `SIGINT`, затем остановка приёма новой работы, ожидание в пределах timeout, закрытие пула БД, выход с кодом 0.

API:

- `/ready` сразу начинает отвечать 503, чтобы балансировщик снял трафик;
- `http.Server.Shutdown` дорабатывает уже принятые запросы (timeout 25 секунд);
- новые соединения не принимаются.

Worker:

- цикл claim останавливается;
- heartbeat текущего job ещё жив;
- FFmpeg, если он запущен, останавливается, временный каталог удаляется;
- job возвращается в `QUEUED` тем же `locked_by` и `attempt`, attempt уменьшается на 1, lock снимается. Остановка релиза не тратит бюджет попыток;
- если условный update не сработал, job остаётся на lease recovery;
- после release закрывается пул.

Падение по `SIGKILL` lease не снимает. Его забирает recovery после таймаута.

### 12. Миграции в Compose

Сервис `migrate` запускает бинарь из образа `music-api` и завершается. `music-api` и `music-worker` стартуют только после `service_completed_successfully`. API при старте схему не меняет. Нужен Docker Compose v2.

Позже тот же бинарь запускается Kubernetes Job или шагом CI/CD, не из каждой реплики.

### 13. Один образ на stage и production

Образ собирается один раз в CI и тегируется SHA коммита. На стенд попадает уже собранный тег. Переменные вроде `DATABASE_URL`, `S3_ENDPOINT`, `S3_PUBLIC_ENDPOINT`, `API_UPSTREAM` задают среду. Отдельной сборки «для stage» нет.

Локальный Compose собирает тег `dev` из исходников. Это путь разработчика, не путь стенда.

### 14. Сознательно отложено

До отдельной команды и отдельного Phase не делать:

- микросервисы, NATS, Kafka, transactional outbox, Redis;
- Elasticsearch, OpenSearch, ClickHouse, `pg_trgm`;
- Kubernetes manifests, Helm, service mesh;
- HLS, DASH, adaptive bitrate, таблицу `audio_variants`;
- рекомендации, соцфункции, DRM, мобильные клиенты;
- автоматический sync и scheduler;
- аутентификацию и авторизацию;
- сбор сиротских объектов;
- CI/CD pipeline;
- реальные CatalogProvider и AudioProvider;
- загрузку обложек и фото артистов. Колонки `image_object_key` и `cover_object_key` есть и остаются пустыми;
- точность неполной даты релиза (`year` / `month`). До реального provider хранится `DATE` или NULL.

Auth — принятый риск только для локального Compose. Общий stage не публикуется в интернет, пока admin API открыт.

## Проверка master prompt

Противоречий в доменной модели Artist / Release / Recording / ReleaseTrack нет. Отдельные места уточнены, без смены границ системы:

| Место | Что сделано |
| --- | --- |
| Один job на весь artist и долгий FFmpeg | Один тип `artist_import`, heartbeat и таймаут процесса. Дробление на audio-jobs отложено |
| `localhost` против `minio:9000` | Внутренний endpoint для Put, публичный для подписи |
| Один образ и `NEXT_PUBLIC_*` | Same-origin proxy, переменная `API_UPSTREAM` читается в runtime |
| Образ без тестов и демо-audio | В worker копируются только короткие fixture-файлы LocalAudioProvider |
| Статус audio на Recording | Не хранится. UI получает вычисляемый `playback_available` |
| README ссылался на `docs/events.md` | Файла событий нет и не будет, пока нет реальной нужды в шине |

Отдельный документ событий не создаётся.
