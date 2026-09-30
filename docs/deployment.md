# Deployment

Локальный запуск с первого дня — контейнеры. На хосте достаточно Docker и Docker Compose v2. Go, Node.js, PostgreSQL, MinIO и FFmpeg на хосте не требуются. Запуск одного процесса из IDE — дополнительный путь отладки.

## Состав Compose

Файл Phase 1: `deploy/compose/docker-compose.yml`.

| Сервис | Образ | Зачем |
| --- | --- | --- |
| `migrate` | `music-api:dev`, команда `migrate` | Один прогон миграций, `restart: "no"` |
| `music-api` | `music-api:dev` | HTTP API |
| `music-worker` | `music-worker:dev` | Очередь и audio |
| `music-web` | `music-web:dev` | UI и proxy `/api/v1` |
| `postgres` | `postgres:18`, не `latest` | БД |
| `minio` | `bitnamilegacy/minio:2025.7.23-debian-12-r5` | Object Storage |
| `minio-init` | тот же образ, клиент `mc` | Создать бакет и CORS, затем выйти |

Сеть одна, DNS-имена равны именам сервисов. Внутри сети API и worker ходят в `postgres:5432` и `minio:9000`.

`music-api` и `music-worker` зависят от `migrate` с условием `service_completed_successfully` и от готовности postgres. `migrate` зависит от готовности postgres. `minio-init` завершается до того, как worker начнёт писать объекты: worker зависит от успешного `minio-init`.

`container_name` не задаётся. Иначе `docker compose up --scale music-worker=2` не работает. Порт worker на хост не публикуется по той же причине. Health worker слушает `8081` только в сети Compose.

Порты на хост, для браузера и отладки:

| Сервис | Порт |
| --- | --- |
| `music-web` | `3000` |
| `music-api` | `8080` |
| `postgres` | `5432` |
| `minio` | `9000` (S3), `9001` (консоль) |

Рестарт `unless-stopped` у долгоживущих сервисов. `migrate` и `minio-init` не перезапускаются по кругу.

Тома только у состояния инфраструктуры:

- `postgres_data` смонтирован в `/var/lib/postgresql`. У официального образа PostgreSQL 18 данные лежат в `/var/lib/postgresql/18/docker`, а объявленный volume — родительский каталог. Монтирование старого пути `/var/lib/postgresql/data` на этом образе не сохраняет кластер.
- `minio_data` смонтирован в `/bitnami/minio/data`. Так этот образ Bitnami хранит данные.

У API, worker и web постоянных томов нет. Демо-audio для локального provider копируется в образ worker (короткие fixture-файлы), а не монтируется с хоста, чтобы `docker compose up -d` не зависел от раскладки исходников на машине. Тесты в образ не копируются.

Каталог приложения в контейнере считается одноразовым. Падение контейнера не должно оставлять нужных данных нигде, кроме PostgreSQL и MinIO.

## Образы

Multi-stage сборка.

`Dockerfile.api`:

- stage build: Go 1.27.1, `CGO_ENABLED=0`, бинари `api` и `migrate`;
- runtime: минимальный образ без шелла, если так собирается статический бинарь, пользователь не root;
- нет компилятора в runtime, нет git, нет тестов, нет `.env`, нет FFmpeg.

`Dockerfile.worker`:

- тот же исходный код модуля;
- runtime на базе slim-образа, где есть `ffmpeg` и `ffprobe`;
- пользователь не root;
- в образ добавлен только каталог демо-audio;
- нет компилятора Go в runtime.

`frontend/Dockerfile`:

- stage build: Node, `next build`, `output: "standalone"`;
- runtime: только standalone-сервер и статические ассеты, `next dev` отсутствует;
- пользователь не root;
- слушает порт 3000.

`.dockerignore` в корне исключает `.git`, `.env`, `**/.env`, тесты не обязаны быть в ignore для API (они не копируются явным `COPY`), `node_modules`, фронтовые сборки. Секреты в build context не попадают.

Локальные теги `music-api:dev`, `music-worker:dev`, `music-web:dev` мутабельны и остаются на машине разработчика. Их не используют как имя деплоя.

## Конфигурация

Файл `.env.example` в корне. Настоящий `.env` в git не входит (`.gitignore` добавляется в Phase 1).

Переменные:

| Переменная | Назначение |
| --- | --- |
| `DATABASE_URL` | PostgreSQL. Локально `postgres://music:music@postgres:5432/music?sslmode=disable` |
| `POSTGRES_USER` | Пользователь образа Postgres. Локально `music` |
| `POSTGRES_PASSWORD` | Пароль образа Postgres. Локально `music` |
| `POSTGRES_DB` | Имя базы. Локально `music` |
| `HTTP_ADDR` | Адрес API, `:8080` |
| `WORKER_HEALTH_ADDR` | Адрес проб worker, `:8081` |
| `WORKER_INSTANCE_ID` | Необязательный UUID. Пусто — сгенерировать при старте |
| `S3_ENDPOINT` | Внутренний адрес для Put, `http://minio:9000` |
| `S3_PUBLIC_ENDPOINT` | Адрес в подписи для браузера, `http://localhost:9000` |
| `S3_BUCKET` | `music` |
| `S3_REGION` | `us-east-1` (формальное значение для MinIO) |
| `S3_ACCESS_KEY` | Локальный ключ MinIO |
| `S3_SECRET_KEY` | Локальный секрет MinIO |
| `S3_USE_SSL` | `false` локально |
| `S3_USE_PATH_STYLE` | `true` для MinIO |
| `AUDIO_TEMP_DIR` | `/tmp/music-audio` |
| `AUDIO_MAX_BYTES` | `209715200` |
| `AUDIO_PROCESS_TIMEOUT` | `10m` |
| `PLAYBACK_URL_TTL` | `1h` |
| `LOCAL_AUDIO_DIR` | Путь fixture внутри worker |
| `API_UPSTREAM` | Для web: `http://music-api:8080` |
| `LEASE_TIMEOUT` | `2m` |
| `LEASE_HEARTBEAT` | `30s` |

Один образ читает разные значения этих переменных на local, test, stage и production. Отдельный образ под среду не собирается.

Локальные пароли из `.env.example` годятся только для Compose на своей машине. На stage те же ключи приходят из секретов окружения, не из репозитория и не из слоя образа.

`S3_ENDPOINT` и `S3_PUBLIC_ENDPOINT` различаются. Подпись — локальный HMAC, API не обязан достучаться до публичного адреса, чтобы выписать URL. Загрузка worker-ом идёт на внутренний адрес. Host в подписи совпадает с host, который откроет браузер.

## MinIO

Официальные образы `minio/minio` и `minio/mc` сняты с Docker Hub, тот же релиз на `quay.io` отвечает 401, а `dl.min.io` отдаёт 410. Локальный стек использует закреплённый образ `bitnamilegacy/minio:2025.7.23-debian-12-r5`. Это тот же MinIO, с клиентом `mc` внутри. Команда сервера остаётся на entrypoint образа: он сам поднимает API на 9000 и консоль на 9001.

`minio-init` создаёт бакет `music`, если его нет, и не включает публичное чтение. CORS этой версии MinIO задаётся переменной `MINIO_API_CORS_ALLOW_ORIGIN` на самом сервере: origin локального web — `http://localhost:3000`. Методы чтения — `GET` и `HEAD`.

Объекты пишутся с `Content-Type: audio/mp4` для playable. Чтение только по подписи.

## Миграции

`cmd/migrate` применяет `*.up.sql` из `backend/migrations` по имени файла и выходит с 0. Служебная таблица `schema_migrations` хранит уже применённые версии. Повторный запуск их не исполняет снова. С Phase 2 первая доменная миграция — `0001_catalog.up.sql`.

Несколько API не мигрируют схему при старте. В Compose это отдельный завершившийся сервис. В Kubernetes позже это Job перед выкладкой новых Pod или шаг CI/CD. Образ тот же `music-api`, команда другая.

## Сеть playback

```text
Browser -- :3000 --> music-web -- API_UPSTREAM --> music-api
Browser -- подписанный URL :9000 --> minio
```

Страницы и JSON идут на один origin. Файл — на MinIO. Из-за этого CORS API для браузера не нужен, CORS бакета нужен как защита Range-запросов и будущих клиентов, которые читают медиа через `fetch`.

## Пробы и остановка

API: `GET /health`, `GET /ready` на `8080`.

Worker: те же пути на `8081`.

`/ready` отдаёт 503 с момента получения `SIGTERM`, затем процесс дорабатывает запросы (API, до 25 секунд) или снимает lock текущего job (worker) и закрывает пул. В будущем это `readinessProbe` и `livenessProbe` без изменений бизнес-логики. Liveness смотрит на `/health` и не ходит в базу.

## Логи

JSON в stdout. Поля минимум: `time`, `level`, `msg`. У запросов API — `request_id`. У job — `job_id`. У worker — `worker_instance_id`.

В лог не попадают пароли, ключи S3, токены provider и полные подписанные URL.

Файловых логов приложения нет. Стек Prometheus, Grafana и OpenTelemetry на MVP не поднимается.

## Горизонтальное масштабирование

API не хранит сессию. Несколько реплик стоят за одним адресом, sticky sessions не нужны.

Worker масштабируется числом реплик. Корректность берёт `SKIP LOCKED`, lease и fencing, а не выбор лидера. Два активных импорта одного артиста не создаются ограничением БД.

PostgreSQL и Object Storage в поды приложения не кладутся.

Локальная проверка второй реплики: `docker compose up -d --scale music-worker=2`.

## Stage и registry

Целевая цепочка, без реализации в Phase 0:

```text
commit
  -> CI: go test, go vet, проверки frontend, integration tests
  -> docker build один раз
  -> registry: music-api:git-<sha>, music-worker:git-<sha>, music-web:git-<sha>
  -> stage и production запускают этот тег
```

На стенде нет `git clone`, `go build`, `npm install` и `docker build`. Образ не пересобирается между stage и production.

Тег `latest` не используется как имя выкладки. Откат — смена тега на предыдущий SHA.

## Kubernetes позже

Манифестов в репозитории нет, каталог `deploy/kubernetes` не создаётся, пока не начнётся соответствующий Phase.

Ожидаемая форма, под которую уже пишется приложение:

```text
Ingress
  -> music-web (N)
  -> music-api (N) -> PostgreSQL
                 ^
                 music-worker (N) -> Object Storage
```

Миграции — Job. Секреты — Secrets. Несекретная конфигурация — ConfigMap или env. У контейнеров будут requests и limits. Rolling update опирается на `/ready` и на обработку `SIGTERM`, которая уже есть.

Бизнес-логика под Kubernetes не переписывается.

## Локальные учётные данные

В `.env.example` будут отдельные пользователь и пароль Postgres и пара ключей MinIO со значением вроде `music` / `musicsecret`. Они не являются секретом production. В образе их нет: Compose передаёт их переменными.
