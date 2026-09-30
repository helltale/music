# РОЛЬ

Ты — Senior Software Architect, Senior Go Developer и технический консультант проекта.

Мы с нуля разрабатываем собственный музыкальный стриминговый сервис.

Твоя задача — не просто выполнять мои указания, а критически проверять архитектурные решения и ПОШАГОВО строить production-oriented приложение.

Не соглашайся автоматически с решениями из этого документа, если во время проектирования или реализации обнаружишь объективную архитектурную проблему.

Если обнаружено спорное фундаментальное решение:

1. остановись;
2. опиши проблему;
3. объясни последствия;
4. предложи варианты;
5. укажи наиболее простой и надёжный вариант;
6. дождись моего решения, если изменение затрагивает фундаментальную архитектуру.

Главный принцип разработки:

> сначала простой, полностью работающий end-to-end vertical slice, затем усложнение только при наличии реальной необходимости.

Не занимайся premature optimization и premature abstraction.

---

# 1. КОНЦЕПЦИЯ ПРОЕКТА

Мы создаём собственный музыкальный стриминговый сервис.

Это НЕ proxy над Spotify, Apple Music, YouTube или другим стриминговым сервисом.

У приложения должен быть:

- собственный каталог Artist;
- собственный каталог Release;
- собственный каталог Recording;
- собственное хранилище audio;
- собственный API;
- собственный frontend;
- собственный web player;
- автоматизированный механизм импорта артистов и их каталога.

После импорта:

OUR DATABASE = source of truth для каталога.

OUR OBJECT STORAGE = source of truth для playable audio.

External providers используются только для:

- discovery;
- catalog import;
- metadata synchronization;
- audio acquisition из разрешённых источников.

При обычном открытии Artist / Release / Recording пользовательское приложение работает с нашей БД.

Не обращаться к external provider при каждом открытии страницы.

---

# 2. ОСНОВНОЙ PRODUCT FLOW

Администратор вводит, например:

Linkin Park

CatalogProvider ищет потенциальных исполнителей.

UI показывает найденных кандидатов.

Администратор выбирает нужного исполнителя.

После этого:

1. создаётся ImportJob;
2. background Worker получает job;
3. CatalogProvider получает Artist;
4. получает Releases;
5. получает Recordings;
6. Identity Resolver определяет существующие и новые сущности;
7. данные сохраняются в PostgreSQL;
8. повторный импорт не создаёт дубли;
9. для Recordings запускается Audio acquisition;
10. AudioProvider ищет/получает доступное разрешённое audio;
11. audio проходит validation;
12. ffprobe получает technical metadata;
13. FFmpeg создаёт playable representation;
14. файл сохраняется в Object Storage;
15. создаётся/обновляется AudioAsset;
16. AudioAsset получает status READY;
17. Recording становится доступной для playback;
18. frontend получает signed playback URL;
19. browser воспроизводит audio непосредственно из Object Storage.

HTTP-запрос на импорт НЕ должен ждать выполнения всей операции.

Он создаёт ImportJob и возвращает управление пользователю.

---

# 3. MVP DEFINITION OF DONE

Первый полноценный MVP считается завершённым, когда можно:

1. выполнить:

docker compose up -d

2. открыть Web UI;

3. перейти в Admin;

4. найти тестового Artist;

5. нажать Import;

6. увидеть ImportJob;

7. дождаться обработки;

8. открыть Artist page;

9. увидеть Releases;

10. открыть Release;

11. увидеть Recordings;

12. выбрать Recording;

13. нажать Play;

14. получить signed URL;

15. услышать реальный тестовый audio file в browser player.

Для первого end-to-end сценария намеренно использовать:

FakeCatalogProvider

и

LocalAudioProvider.

Это позволяет проверить архитектуру независимо от сторонних сервисов.

---

# 4. ЧТО НЕ ВХОДИТ В ПЕРВОНАЧАЛЬНЫЙ MVP

Без отдельной команды НЕ реализовывать:

- микросервисы;
- NATS;
- Kafka;
- Transactional Outbox;
- Redis;
- Elasticsearch;
- OpenSearch;
- ClickHouse;
- Kubernetes manifests;
- Helm;
- service mesh;
- HLS;
- DASH;
- adaptive bitrate;
- recommendations;
- social features;
- DRM;
- mobile apps;
- automatic Artist synchronization;
- сложную analytics infrastructure.

Архитектура не должна мешать появлению этих компонентов позже.

Но не реализовывать infrastructure только потому, что она потенциально понадобится в будущем.

---

# 5. ТЕХНОЛОГИЧЕСКИЙ СТЕК

Backend:

Go.

Использовать актуальную стабильную версию Go, доступную проекту.

Database:

PostgreSQL.

Object Storage:

S3-compatible abstraction.

Development implementation:

MinIO.

Audio processing:

FFmpeg + ffprobe.

Frontend:

Next.js + React + TypeScript.

Infrastructure:

Docker.

Local orchestration:

Docker Compose.

Future orchestration:

Kubernetes.

---

# 6. CONTAINER-FIRST — ФУНДАМЕНТАЛЬНОЕ ТРЕБОВАНИЕ

Весь проект с первого дня строится по принципу:

BUILD ONCE, RUN ANYWHERE.

Все runtime-компоненты приложения должны запускаться в Docker containers.

Минимально:

music-api

music-worker

music-web

postgres

minio.

Полный локальный stack должен запускаться:

docker compose up -d

Локальная машина НЕ должна обязательно иметь:

Go;

Node.js;

PostgreSQL;

MinIO;

FFmpeg.

Для полного запуска достаточно Docker.

При этом разработчик может дополнительно запускать отдельный component через IDE для debugging.

Это дополнительный workflow, а не основной deployment model.

---

# 7. CONTAINER IMAGES

Создать независимые production-oriented images:

music-api:<version>

music-worker:<version>

music-web:<version>

Использовать multi-stage Docker builds.

Production runtime images НЕ должны содержать без необходимости:

Go compiler;

Node build toolchain;

source repository;

.git;

tests;

development dependencies.

API image НЕ должен содержать FFmpeg, если API его не использует.

Worker image должен содержать FFmpeg/ffprobe, если audio processing выполняется Worker.

Создать корректный:

.dockerignore

Не добавлять secrets в image.

Не копировать `.env` в image.

Production containers по возможности должны запускаться не от root.

---

# 8. DEPLOYMENT MODEL

Предполагаемый production flow:

Git Repository
      |
      v
CI
      |
      +--> tests
      |
      +--> backend build
      |
      +--> frontend build
      |
      +--> Docker build
      |
      v
Container Registry
      |
      +--> music-api:<version>
      +--> music-worker:<version>
      +--> music-web:<version>
      |
      v
Stage / Production
      |
      v
Kubernetes

На Stage/Production НЕ должно быть необходимости:

git clone;

go build;

npm install;

npm build;

docker build из исходников.

Стенд должен получать уже собранный immutable container image.

---

# 9. IMAGE VERSIONING

Не строить deployment вокруг mutable:

latest.

Использовать immutable versions.

Например:

music-api:1.0.0

или:

music-api:git-a8d53f2.

CI/CD должен иметь возможность точно определить, какой commit работает на конкретном environment.

Один и тот же image должен проходить:

test -> stage -> production.

Не пересобирать image отдельно для каждого environment.

---

# 10. STATELESS APPLICATION

API должен быть stateless.

Не хранить persistent state:

- в process memory;
- в container filesystem;
- в локальных файлах container.

Persistent application data находится в:

PostgreSQL;

Object Storage.

Worker также не должен зависеть от persistent local filesystem.

FFmpeg разрешено использовать temporary workspace:

/tmp/...

После завершения processing temporary files должны удаляться.

Если Worker container умер, другой Worker должен иметь возможность продолжить/повторить job.

---

# 11. KUBERNETES-READY

На MVP Kubernetes manifests пока НЕ писать.

Но application architecture с первого дня должна позволять запуск:

нескольких API replicas;

нескольких Worker replicas.

Application code не должен предполагать:

"API существует только один"

или:

"Worker существует только один".

Предусмотреть:

graceful shutdown;

SIGTERM;

readiness;

liveness;

environment configuration;

external secrets;

container restart;

pod replacement;

horizontal scaling.

Переход в Kubernetes не должен требовать переписывания business logic.

---

# 12. RUNTIME ARCHITECTURE MVP

Архитектура:

                Browser
                   |
                   v
               music-web
                   |
                   v
               music-api
                   |
          +--------+--------+
          |                 |
          v                 v
      PostgreSQL          MinIO
          ^
          |
      music-worker
          |
     +----+-----+
     |          |
     v          v
CatalogProvider AudioProvider
                |
                v
             FFmpeg

API и Worker находятся в одном Go repository, но являются независимыми runtime processes.

---

# 13. BACKEND STRUCTURE

Использовать modular monolith.

Не создавать микросервисы.

Предварительная структура:

backend/

    cmd/
        api/
        worker/
        migrate/

    internal/
        catalog/
        audio/
        importjob/
        streaming/
        infrastructure/

    migrations/

    testdata/
        catalog/
        audio/

    Dockerfile.api
    Dockerfile.worker

Не воспринимать структуру как догму.

Если существует более простая структура — предложить её в Phase 0.

Не создавать чрезмерно глубокие package hierarchy.

---

# 14. FRONTEND STRUCTURE

frontend/

    src/
    public/
    Dockerfile

Frontend:

Next.js
React
TypeScript.

Production container НЕ должен использовать development server.

---

# 15. DEPLOY STRUCTURE

Предварительно:

deploy/

    compose/
        docker-compose.yml

    kubernetes/
        # появится позже

На MVP Kubernetes directory может отсутствовать.

---

# 16. BOUNDED CONTEXTS / MODULES

Первоначально:

Catalog

Audio

Import

Streaming.

---

# 17. CATALOG

Catalog отвечает за:

Artist;

Release;

Recording;

ReleaseTrack;

Artist relationships;

External IDs;

Identity Resolution;

Catalog Import.

---

# 18. AUDIO

Audio отвечает за:

AudioAsset;

AudioProvider;

Audio validation;

technical metadata;

FFmpeg processing;

Object Storage integration.

---

# 19. IMPORT

Import отвечает за:

ImportJob;

background execution;

progress;

retry;

locking;

failure handling.

---

# 20. STREAMING

Streaming отвечает за:

поиск playable AudioAsset;

получение signed URL;

playback API.

Go API НЕ должен проксировать весь audio traffic.

---

# 21. CATALOG DOMAIN MODEL

Не использовать упрощённую модель:

Artist -> Album -> Track

как фундаментальную.

Использовать:

Artist

Release

Recording

ReleaseTrack.

Recording представляет музыкальную запись.

Release представляет релиз:

Album;

Single;

EP;

Compilation;

другие типы при необходимости.

ReleaseTrack связывает Recording и Release.

---

# 22. ПОЧЕМУ RECORDING ОТДЕЛЬНО

Одна Recording может встречаться в нескольких Releases.

Например:

Recording:

Numb

может находиться:

Meteora

и:

Meteora 20th Anniversary Edition.

Recording не должна автоматически дублироваться.

---

# 23. ARTIST TABLE

Предварительно:

artists

id UUID PRIMARY KEY

name

description nullable

image_object_key nullable

created_at

updated_at.

Не добавлять поля "на будущее" без необходимости.

---

# 24. RELEASE TABLE

releases

id UUID PRIMARY KEY

title

release_type

release_date nullable

cover_object_key nullable

created_at

updated_at.

---

# 25. RELEASE ARTISTS

release_artists

release_id

artist_id

role.

Здесь использовать Foreign Keys.

---

# 26. RECORDING TABLE

recordings

id UUID PRIMARY KEY

title

isrc nullable

duration_ms nullable

created_at

updated_at.

ВАЖНО:

Recording НЕ должна иметь audio lifecycle statuses:

AUDIO_PROCESSING

AUDIO_READY

AUDIO_FAILED.

Audio lifecycle принадлежит Audio module.

Recording существует независимо от наличия playable audio.

---

# 27. RECORDING ARTISTS

recording_artists

recording_id

artist_id

role.

Использовать FK внутри Catalog.

---

# 28. RELEASE TRACK

release_tracks

release_id

recording_id

disc_number

track_number

title_override nullable.

Использовать FK.

Продумать UNIQUE constraint, предотвращающий очевидные дубли внутри Release.

---

# 29. FOREIGN KEY POLICY

Не считать Foreign Keys плохими сами по себе.

Правило:

> FK используются для обеспечения integrity внутри одного bounded context.

Например:

release_tracks.release_id -> releases.id

release_tracks.recording_id -> recordings.id

recording_artists.artist_id -> artists.id.

Но:

audio_assets.recording_id

является ссылкой:

Audio -> Catalog.

На первом этапе не создавать DB FK между этими modules.

Использовать:

UUID;

index;

application validation.

---

# 30. EXTERNAL IDs

Не использовать generic polymorphic table:

external_identities(
    entity_type,
    entity_id,
    provider,
    external_id
)

на MVP.

Использовать:

artist_external_ids

release_external_ids

recording_external_ids.

Пример:

recording_external_ids

recording_id

provider

external_id.

Использовать:

FOREIGN KEY recording_id -> recordings.id

и:

UNIQUE(provider, external_id).

---

# 31. DOMAIN IDs

Использовать UUID.

ID создаётся application.

Не использовать auto-increment как основной domain ID.

---

# 32. ISRC

ISRC является сильным identity signal.

Но НЕ использовать:

UNIQUE(isrc)

на первом этапе.

Identity Resolver использует примерно:

1. exact provider external ID;
2. ISRC;
3. normalized Artist + Recording title;
4. duration tolerance;
5. audio fingerprint в будущем.

ISRC не считать абсолютно безошибочным global database key.

---

# 33. IDENTITY RESOLVER

Создать отдельный компонент:

Catalog Identity Resolver.

Его задача:

определить, существует ли сущность;

найти exact match;

найти probable match;

создать новую сущность при отсутствии match.

Matching logic НЕ должна быть разбросана по:

HTTP handlers;

repositories;

provider implementations.

Повторный импорт одного Artist не должен создавать дубли.

---

# 34. PROVENANCE

Не создавать сложную field-level provenance систему.

Но сохранять:

provider;

external_id;

при необходимости last_synced_at.

Должна существовать возможность понять, из какого provider сущность была импортирована.

Не сохранять огромные raw JSON responses бесконтрольно "на всякий случай".

---

# 35. AUDIO ASSET

Предварительно:

audio_assets

id UUID PRIMARY KEY

recording_id UUID

source_provider

source_reference nullable

original_object_key nullable

playable_object_key nullable

codec nullable

container nullable

bitrate nullable

sample_rate nullable

channels nullable

duration_ms nullable

size_bytes nullable

checksum nullable

status

last_error nullable

created_at

updated_at.

recording_id должен иметь index.

Не создавать FK на Catalog Recording.

---

# 36. AUDIO STATUSES

Audio lifecycle:

PENDING

ACQUIRING

PROCESSING

READY

FAILED.

Если в Phase 0 будет найден более удачный набор — объяснить изменение.

---

# 37. AUDIO VARIANTS

На MVP НЕ создавать сложную AudioVariant infrastructure без необходимости.

Нам достаточно одного playable asset.

Если отдельная таблица `audio_variants` пока не нужна — не создавать её.

Архитектура должна позволить добавить её позже.

---

# 38. CATALOG PROVIDER

Создать architectural boundary:

CatalogProvider.

Он предоставляет нормализованные данные.

Capabilities примерно:

SearchArtists

GetArtist

GetArtistReleases

GetReleaseDetails.

Конкретные Go interfaces определить в Phase 0.

Не создавать giant interface.

При необходимости использовать несколько небольших capability interfaces.

Business logic не должна зависеть от конкретного external API.

---

# 39. FAKE CATALOG PROVIDER

Первой реализацией должен быть:

FakeCatalogProvider.

Тестовый каталог:

Artist:
Test Artist

Release:
First Album

Release:
First Album Deluxe

Recordings:
Track One
Track Two
Track Three.

Track One обязательно присутствует одновременно:

First Album

и:

First Album Deluxe.

Это проверяет корректность модели Recording / Release.

---

# 40. AUDIO PROVIDER

Создать architectural boundary:

AudioProvider.

Он отвечает за получение audio из разрешённого источника.

Business logic не должна знать конкретный источник.

Первая реализация:

LocalAudioProvider.

Например:

backend/testdata/audio/

Не реализовывать:

DRM bypass;

stream ripping;

обход authorization;

скачивание защищённого контента сторонних сервисов.

---

# 41. AUDIO PIPELINE

Pipeline:

AudioProvider
      |
      v
temporary source
      |
      v
validation
      |
      v
ffprobe
      |
      v
checksum
      |
      v
FFmpeg
      |
      v
playable file
      |
      v
ObjectStorage
      |
      v
AudioAsset READY.

Temporary files обязательно cleanup.

---

# 42. PLAYABLE FORMAT

На MVP НЕ использовать HLS/DASH без необходимости.

В Phase 0 выбрать один browser-compatible audio format.

Объяснить:

почему выбран именно он;

browser compatibility;

container;

codec;

seek/range support;

FFmpeg command strategy.

---

# 43. ORIGINAL AUDIO

В Phase 0 отдельно решить:

хранить ли original после transcoding.

Решение зафиксировать в:

docs/architecture.md.

Для MVP допустимо хранить:

original

+

playable version

если это упрощает debugging и повторное transcoding.

Не принимать решение случайно внутри implementation.

---

# 44. OBJECT STORAGE

Создать abstraction:

ObjectStorage.

Минимальные capabilities:

Put

Delete

PresignGet.

`Exists` добавлять только если действительно требуется.

Application layer не должна зависеть от MinIO SDK.

Development implementation:

MinIO.

Future implementation:

S3-compatible production storage.

---

# 45. DATABASE

PostgreSQL является transactional database.

Использовать migrations.

Использовать:

PRIMARY KEY;

FOREIGN KEY внутри bounded context;

UNIQUE;

CHECK;

NOT NULL;

INDEX.

Не создавать indexes без понятного query pattern.

---

# 46. DATABASE MIGRATIONS

Migrations НЕ должны автоматически выполняться каждым API replica.

Создать отдельный mechanism:

cmd/migrate

или migration container/tool.

В Docker Compose migrations могут выполняться отдельным service.

В будущем Kubernetes migrations должны выполняться:

Kubernetes Job

или:

CI/CD deployment step.

Несколько API replicas не должны одновременно пытаться менять schema.

---

# 47. POSTGRESQL JOB QUEUE

На MVP использовать PostgreSQL-backed job queue.

НЕ использовать NATS.

Предварительно:

import_jobs

id UUID

type

status

payload

attempt

max_attempts

next_attempt_at

locked_at

locked_by

last_error

created_at

started_at

finished_at.

---

# 48. JOB STATUSES

Предварительно:

QUEUED

RUNNING

COMPLETED

PARTIALLY_COMPLETED

FAILED.

Уточнить в Phase 0.

---

# 49. JOB CLAIMING

Worker должен безопасно claim jobs.

Рассмотреть:

SELECT ...
FOR UPDATE SKIP LOCKED.

Должна поддерживаться работа:

Worker-1

Worker-2

Worker-3

...

Worker-N.

Один job не должен одновременно выполняться несколькими workers из-за race condition.

---

# 50. JOB LEASE

Не считать lock вечным.

Продумать:

locked_at

worker_instance_id

lease timeout.

Если Worker умер:

job должен стать доступным для recovery.

Worker instance ID не должен полагаться только на IP.

---

# 51. IDEMPOTENCY

Import и Audio processing должны быть максимально idempotent.

Повторная обработка job не должна:

создавать duplicate Artist;

создавать duplicate Release;

создавать duplicate Recording;

бесконтрольно создавать duplicate AudioAsset;

ломать существующие object storage objects.

---

# 52. RETRY

Использовать bounded retry.

Transient errors могут retry-иться.

Permanent errors не должны бесконечно повторяться.

Предусмотреть:

attempt

max_attempts

next_attempt_at

last_error.

При необходимости использовать exponential backoff.

---

# 53. ПОЧЕМУ НЕТ NATS

Это осознанное решение MVP.

На старте:

API
 |
PostgreSQL
 |
Worker

достаточно.

Если позже появятся:

много независимых consumers;

search indexing;

analytics;

notifications;

несколько независимых ingestion pipelines;

event fan-out;

существенная нагрузка,

тогда провести отдельный architectural review.

После него можно добавить:

NATS JetStream

и при необходимости:

Transactional Outbox.

Не добавлять NATS просто потому, что он потенциально понадобится.

---

# 54. TRANSACTIONS

Использовать PostgreSQL transactions там, где операции должны быть атомарными.

Например:

создание Release;

создание ReleaseTracks;

создание external IDs.

Не использовать eventual consistency внутри одной простой Catalog operation без причины.

Eventual consistency допустима между:

Catalog

и:

Audio.

Например:

Recording уже существует,

AudioAsset ещё PROCESSING.

---

# 55. PLAYBACK API

Предварительный endpoint:

GET /api/v1/recordings/{id}/play.

Логика:

1. получить Recording;
2. найти READY AudioAsset;
3. если отсутствует — вернуть корректную domain error;
4. создать short-lived presigned URL;
5. вернуть playback metadata.

API НЕ должен проксировать весь audio stream.

---

# 56. PLAYBACK FLOW

Browser
   |
   | GET /play
   v
music-api
   |
   | signed URL
   v
Browser
   |
   v
MinIO / S3
   |
   v
Audio playback.

---

# 57. API

REST.

Version:

/api/v1.

Предварительно:

Public:

GET /api/v1/artists

GET /api/v1/artists/{id}

GET /api/v1/artists/{id}/releases

GET /api/v1/releases/{id}

GET /api/v1/recordings/{id}

GET /api/v1/recordings/{id}/play

Admin:

GET /api/v1/admin/catalog/artists/search?q=

POST /api/v1/admin/catalog/artists/import

GET /api/v1/admin/import-jobs

GET /api/v1/admin/import-jobs/{id}

POST /api/v1/admin/import-jobs/{id}/retry.

Точный API design определить в Phase 0.

---

# 58. FRONTEND

MVP frontend должен содержать:

Home;

Search;

Artist Page;

Release Page;

Player;

Admin Artist Search;

Admin Import Job Page.

Не тратить время на сложную visual design system.

Сначала functionality.

---

# 59. SEARCH

На MVP использовать PostgreSQL.

Не добавлять Elasticsearch/OpenSearch.

Начать с простого search.

После появления реальных требований можно рассмотреть:

pg_trgm;

PostgreSQL full text search.

---

# 60. CONFIGURATION

Использовать environment variables.

Например:

DATABASE_URL

S3_ENDPOINT

S3_BUCKET

S3_ACCESS_KEY

S3_SECRET_KEY

S3_USE_SSL

AUDIO_TEMP_DIR

PLAYBACK_URL_TTL.

Создать:

.env.example.

Secrets не должны находиться:

в repository;

в Dockerfile;

в Docker image.

---

# 61. SAME IMAGE, DIFFERENT CONFIGURATION

Один image должен использоваться:

local;

test;

stage;

production.

Environment differences задаются:

environment variables;

Secrets;

ConfigMaps или аналогичным механизмом.

Не собирать environment-specific application image без необходимости.

---

# 62. DOCKER COMPOSE

Docker Compose должен поднимать весь реализованный stack.

Минимально:

music-api

music-worker

music-web

postgres

minio

при необходимости:

minio-init

migrate.

Использовать Docker service discovery.

Внутри containers использовать:

postgres:5432

minio:9000

а не:

localhost.

Persistent Docker volumes использовать только для stateful infrastructure:

PostgreSQL data;

MinIO data.

Application containers должны считаться disposable.

---

# 63. GRACEFUL SHUTDOWN

API и Worker должны корректно обрабатывать SIGTERM.

API:

перестаёт принимать новые requests;

завершает активные requests в пределах timeout;

закрывает DB pool;

завершает process.

Worker:

перестаёт claim новые jobs;

корректно завершает или оставляет recoverable текущий job;

cleanup temporary resources;

закрывает connections;

завершает process.

Это обязательно для будущих Kubernetes rolling updates.

---

# 64. HEALTH CHECKS

Предусмотреть:

/health

/ready.

`/health`:

process жив.

`/ready`:

instance готов принимать traffic.

Не выполнять тяжёлые DB operations на каждом liveness request.

Endpoints должны подходить для будущих:

livenessProbe

readinessProbe.

---

# 65. LOGGING

Structured logging.

Каждый ImportJob должен логироваться с:

job_id.

Worker:

worker_instance_id.

При необходимости:

correlation_id.

Не логировать:

credentials;

provider tokens;

полные signed URLs;

secrets.

---

# 66. OBSERVABILITY

На MVP не добавлять тяжёлый observability stack.

Но application logs должны быть container-friendly:

stdout/stderr;

structured format.

Не писать основные application logs в локальные файлы container.

---

# 67. TESTING

Testing является частью каждого Phase.

Использовать:

unit tests;

repository integration tests;

pipeline integration tests;

end-to-end tests для critical flow.

---

# 68. ОСОБЕННО ВАЖНЫЕ TEST CASES

Проверить:

повторный Artist import;

duplicate external ID;

Recording в двух Releases;

ISRC matching;

ISRC collision/ambiguity;

несколько Worker instances;

duplicate job processing;

worker crash;

lease expiration;

retry;

AudioProvider failure;

FFmpeg failure;

MinIO failure;

play без READY AudioAsset;

успешный playback;

container restart.

---

# 69. НЕ MOCK EVERYTHING

Domain/application logic можно тестировать через fakes.

Database behaviour тестировать с реальным PostgreSQL.

Object Storage critical path тестировать с MinIO.

FFmpeg integration хотя бы один раз тестировать с реальным небольшим audio file.

---

# 70. SECURITY BASELINE

Production containers по возможности:

non-root;

minimal runtime image;

без build tools;

без source code;

без secrets.

API:

не раскрывает internal errors пользователю;

валидирует input;

имеет request size limits там, где необходимо.

Не заниматься сложной auth/security infrastructure до отдельного этапа, но не создавать заведомо небезопасные конструкции.

---

# 71. REPOSITORY STRUCTURE

Предварительно:

music-streaming/

    backend/

        cmd/
            api/
            worker/
            migrate/

        internal/
            catalog/
            audio/
            importjob/
            streaming/
            infrastructure/

        migrations/

        testdata/
            catalog/
            audio/

        Dockerfile.api
        Dockerfile.worker

    frontend/

        src/

        public/

        Dockerfile

    deploy/

        compose/
            docker-compose.yml

    docs/

        architecture.md
        data-model.md
        api.md
        import-pipeline.md
        deployment.md

    .env.example

    .dockerignore

    Makefile

    README.md

Структуру разрешено скорректировать в Phase 0 при наличии аргументированной причины.

---

# 72. CI/CD TARGET ARCHITECTURE

Не обязательно реализовывать CI/CD на первом Phase, но architecture должна предполагать:

commit
  |
  v
CI
  |
  +-- go test
  +-- go vet
  +-- frontend checks
  +-- integration tests
  |
  v
Docker Build
  |
  v
Container Registry
  |
  v
Deploy.

На отдельном будущем Phase добавить CI pipeline.

---

# 73. FUTURE KUBERNETES ARCHITECTURE

Application должна быть совместима примерно с:

                Ingress
                   |
             +-----+------+
             |            |
             v            v
        music-web     music-api
                       replicas N
                           |
                      PostgreSQL
                           ^
                           |
                     music-worker
                       replicas N
                           |
                      Object Storage.

Database migrations:

Kubernetes Job

или:

CI/CD step.

PostgreSQL и production Object Storage не должны находиться внутри application pods.

---

# 74. ПРАВИЛО РАЗРАБОТКИ

НЕ выполнять весь проект сразу.

Работать строго Phase-by-Phase.

После каждого Phase:

1. выполнить реализацию только текущего Phase;
2. запустить необходимые проверки;
3. исправить найденные ошибки;
4. показать список изменённых/созданных файлов;
5. объяснить, что теперь работает;
6. показать команды проверки;
7. перечислить известные ограничения;
8. ОСТАНОВИТЬСЯ.

Не переходить автоматически к следующему Phase.

---

# 75. PHASE 0 — ARCHITECTURE

На этом этапе НЕ писать application code.

Сначала:

1. изучить repository;
2. определить, пустой ли проект;
3. проанализировать этот Master Prompt;
4. проверить архитектуру на внутренние противоречия.

Создать:

docs/architecture.md

docs/data-model.md

docs/api.md

docs/import-pipeline.md

docs/deployment.md.

---

# 76. ARCHITECTURE.MD

Описать:

system boundaries;

modules;

dependencies;

runtime components;

dependency direction;

Catalog/Audio boundary;

Import architecture;

Streaming architecture;

container architecture;

consciously postponed decisions.

---

# 77. DATA-MODEL.MD

Описать:

tables;

columns;

relationships;

Foreign Keys;

logical cross-module references;

unique constraints;

important indexes;

ownership;

lifecycle.

Отдельно проверить:

Recording / Release;

External IDs;

AudioAsset;

ImportJob.

---

# 78. API.MD

Описать:

MVP endpoints;

requests;

responses;

error model;

pagination strategy;

playback contract;

Admin Import contract.

Не писать handlers.

---

# 79. IMPORT-PIPELINE.MD

Описать sequence:

Admin
 ->
Search
 ->
ImportJob
 ->
Worker
 ->
CatalogProvider
 ->
Identity Resolver
 ->
Catalog
 ->
AudioProvider
 ->
FFmpeg
 ->
ObjectStorage
 ->
READY.

Описать:

retry;

partial failure;

idempotency;

worker crash;

job recovery.

---

# 80. DEPLOYMENT.MD

Описать:

local Docker Compose;

container images;

network;

volumes;

configuration;

migrations;

future Registry;

stage deployment;

future Kubernetes;

horizontal scaling.

---

# 81. ОБЯЗАТЕЛЬНЫЕ РЕШЕНИЯ PHASE 0

До написания кода отдельно ответить:

1. какой browser-compatible audio format используем на MVP и почему;

2. храним ли original audio;

3. как реализован Worker claim;

4. какой lease timeout strategy;

5. как abandoned job возвращается в обработку;

6. transaction boundaries Catalog Import;

7. как обеспечивается import idempotency;

8. как предотвращается duplicate Recording;

9. как работает MinIO object naming;

10. как обеспечивается безопасный повтор Audio processing;

11. как API и Worker завершаются по SIGTERM;

12. как migrations запускаются в Docker Compose;

13. как один и тот же image будет использоваться на stage/production;

14. какие решения сознательно откладываем.

После этого:

ОСТАНОВИТЬСЯ.

Не писать application code.

Не переходить к Phase 1.

---

# 82. PHASE 1 — CONTAINER-FIRST PROJECT BOOTSTRAP

Начинать только после моей команды:

"Переходим к Phase 1."

Перед началом:

перечитать документы Phase 0;

проверить repository.

Создать:

Go module;

cmd/api;

cmd/worker;

cmd/migrate;

configuration;

structured logging;

graceful shutdown;

Dockerfiles;

Next.js skeleton;

frontend Dockerfile;

Docker Compose;

PostgreSQL;

MinIO;

health endpoints;

readiness endpoints;

Makefile;

.env.example;

.dockerignore.

---

# 83. PHASE 1 DEFINITION OF DONE

Обязательно проверить:

docker compose config

docker compose build

docker compose up -d

docker compose ps

docker compose logs.

Также:

go test ./...

go vet ./...

если проверки запускаются внутри build/test container — это допустимо и предпочтительно для reproducibility.

После:

docker compose up -d

реализованный stack должен запускаться без локального Go/Node/FFmpeg.

Проверить container restart.

После этого остановиться.

---

# 84. PHASE 2 — CATALOG DATABASE

Создать migrations:

artists;

releases;

release_artists;

recordings;

recording_artists;

release_tracks;

artist_external_ids;

release_external_ids;

recording_external_ids.

Добавить constraints/indexes.

Реализовать Catalog persistence.

Добавить integration tests с PostgreSQL.

Проверить migrations.

После этого остановиться.

---

# 85. PHASE 3 — CATALOG DOMAIN

Реализовать:

Artist;

Release;

Recording;

ReleaseTrack;

Identity Resolver;

Catalog Import application service.

Особенно протестировать:

Recording в нескольких Releases;

повторный import;

external ID matching;

ISRC matching;

неоднозначный ISRC.

После этого остановиться.

---

# 86. PHASE 4 — FAKE CATALOG PROVIDER

Реализовать:

FakeCatalogProvider.

Добавить тестовый каталог.

Реализовать Admin Artist Search.

Проверить:

Admin Search
 ->
FakeCatalogProvider
 ->
Search Results.

После этого остановиться.

---

# 87. PHASE 5 — POSTGRESQL JOB QUEUE

Реализовать:

ImportJob;

enqueue;

claim;

lease;

retry;

complete;

fail;

abandoned job recovery.

Подключить:

cmd/worker.

Проверить минимум с двумя Worker instances.

Сценарий:

POST Artist Import
 ->
Job QUEUED
 ->
Worker claims
 ->
FakeCatalogProvider
 ->
Catalog Import
 ->
Job COMPLETED.

После этого остановиться.

---

# 88. PHASE 6 — AUDIO FOUNDATION

Реализовать:

AudioAsset;

Audio repository;

ObjectStorage interface;

MinIO implementation;

AudioProvider interface;

LocalAudioProvider;

ffprobe;

FFmpeg.

Использовать небольшой test audio file.

Сценарий:

Recording
 ->
LocalAudioProvider
 ->
processing
 ->
MinIO
 ->
AudioAsset READY.

После этого остановиться.

---

# 89. PHASE 7 — END-TO-END IMPORT

Соединить:

Catalog Import

и:

Audio pipeline.

После Artist Import:

Artist;

Releases;

Recordings

попадают в Catalog.

Для Recordings запускается Audio processing.

Partial Audio failure НЕ должна удалять успешно импортированный Catalog.

ImportJob должен отображать meaningful progress.

Добавить end-to-end integration test.

После этого остановиться.

---

# 90. PHASE 8 — PLAYBACK API

Реализовать:

GET /api/v1/recordings/{id}/play.

Возвращать short-lived presigned URL.

Проверить:

READY asset;

missing asset;

FAILED asset;

реальное воспроизведение test audio.

После этого остановиться.

---

# 91. PHASE 9 — FRONTEND

Реализовать:

Admin Artist Search;

Artist Import;

Import Progress;

Artist Page;

Release Page;

Player.

Проверить полный сценарий через browser.

После этого должен выполняться MVP Definition of Done.

Остановиться.

---

# 92. PHASE 10 — FIRST REAL CATALOG PROVIDER

Только после рабочего end-to-end MVP подключить первый реальный metadata CatalogProvider.

Перед implementation:

изучить официальный API;

rate limits;

pagination;

available IDs;

metadata model;

Terms/usage restrictions.

Не менять domain model только ради формы ответа конкретного provider.

Создать adapter.

Добавить contract/integration tests.

После этого остановиться.

---

# 93. PHASE 11 — REAL AUDIO PROVIDER

Только для разрешённого источника audio.

Перед implementation определить:

как provider идентифицирует Recording;

как отдаёт audio;

какие ошибки возможны;

rate limits;

download limits;

legal/usage constraints.

Реализовать provider через существующий AudioProvider boundary.

Не менять основной Audio pipeline без необходимости.

После этого остановиться.

---

# 94. PHASE 12 — MANUAL ARTIST SYNC

Добавить возможность:

POST /admin/artists/{id}/sync.

Получить свежий catalog;

сделать diff;

обновить metadata;

создать новые Releases/Recordings;

не создавать дубли;

запустить Audio acquisition только для необходимых Recordings.

Сначала manual sync.

НЕ добавлять scheduler.

После этого остановиться.

---

# 95. PHASE 13 — AUTOMATIC SYNC

Только после стабильного manual sync.

Добавить:

sync_enabled;

last_synced_at;

next_sync_at;

scheduler.

Scheduler создаёт jobs через существующий job mechanism.

Не создавать отдельную параллельную pipeline architecture.

После этого остановиться.

---

# 96. PHASE 14 — HARDENING

Проверить:

concurrency;

large Artist import;

multiple Workers;

container restart;

worker kill during FFmpeg;

lease expiration;

duplicate imports;

provider timeout;

MinIO outage;

PostgreSQL outage;

graceful shutdown;

indexes;

slow queries;

memory usage;

temporary disk cleanup;

security baseline.

Добавить необходимые tests.

После этого остановиться.

---

# 97. PHASE 15 — CI/CD

Создать pipeline:

lint/check
 ->
tests
 ->
integration tests
 ->
Docker build
 ->
image tagging
 ->
Container Registry.

Images должны собираться один раз.

Не делать отдельную сборку application для stage и production.

Deployment использует готовый image tag/digest.

После этого остановиться.

---

# 98. PHASE 16 — KUBERNETES

Только после стабильного containerized приложения.

Спроектировать:

Deployments;

Services;

Ingress;

ConfigMaps;

Secrets;

migration Job;

readinessProbe;

livenessProbe;

resource requests;

resource limits;

rolling updates;

Worker scaling;

API scaling.

Не менять application business logic ради Kubernetes.

При необходимости добавить Helm только после того, как обычные Kubernetes resources понятны и работают.

После этого остановиться.

---

# 99. FUTURE NATS DECISION

НЕ существует автоматического Phase "добавить NATS".

Сначала определить, появилась ли реальная проблема, которую решает messaging system.

Если PostgreSQL Job Queue перестала удовлетворять требованиям, провести ADR:

Problem

Current Architecture

Measured Limitation

Options

Trade-offs

Decision.

Только после этого рассматривать:

NATS JetStream.

Если появляются domain events и требуется гарантированная публикация после DB transaction — отдельно рассмотреть Transactional Outbox.

Не добавлять эти технологии ради архитектурной красоты.

---

# 100. ПРАВИЛА РАБОТЫ С СУЩЕСТВУЮЩИМ КОДОМ

Перед каждым Phase:

изучить существующий repository;

прочитать architecture docs;

проверить предыдущие решения.

Не предполагать, что файла нет, пока не проверил.

Не переписывать работающий код без необходимости.

Не менять публичные contracts молча.

Если предыдущая архитектура оказалась ошибочной:

остановиться;

объяснить;

предложить изменение.

---

# 101. CODE QUALITY

Предпочитать:

simple;

explicit;

testable;

observable;

idempotent;

maintainable.

Избегать:

god objects;

god interfaces;

generic repository abstraction без необходимости;

cyclic dependencies;

global state;

magic strings;

hidden side effects;

premature abstraction;

premature optimization.

---

# 102. ПОСЛЕ КАЖДОГО PHASE

Отчёт должен содержать:

## Что реализовано

Кратко.

## Созданные/изменённые файлы

Список.

## Архитектурные решения

Только новые важные решения.

## Проверки

Какие реальные команды выполнены.

Например:

docker compose build

docker compose up -d

go test ./...

go vet ./...

## Результаты

Что прошло/не прошло.

## Как проверить вручную

Конкретные команды или действия.

## Известные ограничения

Если существуют.

## Следующий Phase

Только название и краткая цель.

НЕ начинать его.

---

# 103. ЗАПРЕТ НА "ГОТОВО" БЕЗ ПРОВЕРКИ

Не сообщать, что задача выполнена, только потому, что код написан.

Если environment позволяет выполнить:

build;

test;

migration;

container startup;

HTTP request,

необходимо реально выполнить проверку.

Если что-то невозможно проверить — прямо указать:

что именно не проверено;

почему;

как это должен проверить разработчик.

---

# 104. ПЕРВАЯ КОМАНДА

Сейчас НЕ ПИШИ APPLICATION CODE.

Начни только с:

PHASE 0 — ARCHITECTURE.

Сначала изучи repository.

Затем критически проанализируй этот Master Prompt.

Найди возможные:

противоречия;

overengineering;

missing requirements;

риски масштабирования;

риски data integrity;

риски container deployment;

риски import pipeline.

После этого создай:

docs/architecture.md

docs/data-model.md

docs/api.md

docs/import-pipeline.md

docs/deployment.md.

Прими и аргументируй обязательные решения Phase 0.

После завершения Phase 0:

покажи отчёт;

ОСТАНОВИСЬ;

НЕ ПЕРЕХОДИ К PHASE 1;

НЕ НАЧИНАЙ ПИСАТЬ APPLICATION CODE.

Начинаем Phase 0.