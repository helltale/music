# Модель данных

PostgreSQL 18. Идентификаторы — UUID, их создаёт приложение (UUID v4). Время — `timestamptz`, пишет его PostgreSQL (`now()`) или приложение в UTC. Деньги и autoincrement в модели нет.

Имена ограничений и индексов ниже — ориентир для миграций Phase 2. Типы статусов — `TEXT` плюс `CHECK`, не `CREATE TYPE`: так проще менять набор значений следующей миграцией.

`updated_at` выставляет приложение при записи. Триггеров на MVP нет.

Владение таблицами:

| Таблицы | Модуль |
| --- | --- |
| `artists`, `releases`, `release_artists`, `recordings`, `recording_artists`, `release_tracks`, `*_external_ids` | Catalog |
| `audio_assets` | Audio |
| `import_jobs` | Import |

FK стоят только внутри Catalog. Логическая ссылка Audio → Catalog описана отдельно и пока без FK.

## Общие правила

- Пустая строка не используется вместо NULL.
- `CHECK (char_length(name) > 0)` и то же для `title` и `external_id`.
- Имена индексов создаются под конкретный запрос из [api.md](api.md) и [import-pipeline.md](import-pipeline.md). «На будущее» индексы не добавляются.
- Поиск по подстроке на MVP — `ILIKE`. При маленьком каталоге планировщик идёт последовательным чтением. `pg_trgm` подключается, когда такой запрос станет медленным.

## artists

Каталог исполнителей.

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `id` | `uuid` | PK |
| `name` | `text` | NOT NULL, длина > 0 |
| `description` | `text` | NULL |
| `image_object_key` | `text` | NULL. На MVP не заполняется |
| `created_at` | `timestamptz` | NOT NULL, default `now()` |
| `updated_at` | `timestamptz` | NOT NULL |

Уникальности по `name` нет: два разных артиста могут называться одинаково.

Запрос списка и поиска: `WHERE name ILIKE` с `ORDER BY name`, `LIMIT/OFFSET`. Отдельный btree по `name` этот шаблон не ускоряет, индекса нет.

## releases

Релиз: album, single, EP, compilation. Упрощения «альбом = единственный контейнер треков» нет.

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `id` | `uuid` | PK |
| `title` | `text` | NOT NULL, длина > 0 |
| `release_type` | `text` | NOT NULL, `CHECK (release_type IN ('album', 'single', 'ep', 'compilation'))` |
| `release_date` | `date` | NULL |
| `cover_object_key` | `text` | NULL. На MVP не заполняется |
| `created_at` | `timestamptz` | NOT NULL, default `now()` |
| `updated_at` | `timestamptz` | NOT NULL |

`release_date` — полный день либо NULL. Год без месяца и дня не кодируется как 1 января. Точность появится вместе с первым реальным provider, если она понадобится.

Список релизов артиста идёт через `release_artists`, сортировка `release_date DESC NULLS LAST, title ASC`.

## release_artists

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `release_id` | `uuid` | NOT NULL, FK → `releases.id` |
| `artist_id` | `uuid` | NOT NULL, FK → `artists.id` |
| `role` | `text` | NOT NULL, `CHECK (role IN ('primary', 'featured'))` |

PK `(release_id, artist_id, role)`.

Индекс по `artist_id` нужен для страницы артиста: найти релизы, где он указан.

У релиза может быть несколько `primary`. Требование «хотя бы один primary» проверяет приложение в транзакции релиза. Триггер на это не вешается.

## recordings

Музыкальная запись. Она не принадлежит одному релизу и не знает, есть ли у неё файл.

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `id` | `uuid` | PK |
| `title` | `text` | NOT NULL, длина > 0 |
| `isrc` | `text` | NULL. Если задан: `CHECK (isrc ~ '^[A-Z]{2}[A-Z0-9]{3}[0-9]{7}$')` |
| `duration_ms` | `integer` | NULL, `CHECK (duration_ms IS NULL OR duration_ms >= 0)` |
| `created_at` | `timestamptz` | NOT NULL, default `now()` |
| `updated_at` | `timestamptz` | NOT NULL |

`UNIQUE (isrc)` нет. Индекс:

```sql
CREATE INDEX recordings_isrc_idx ON recordings (isrc) WHERE isrc IS NOT NULL;
```

Он обслуживает шаг Identity Resolver «найти по ISRC». Неоднозначность — это несколько строк, а не ошибка ограничения.

`duration_ms` — метаданные каталога от provider. Длительность playable-файла живёт в `audio_assets` и может слегка отличаться.

## recording_artists

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `recording_id` | `uuid` | NOT NULL, FK → `recordings.id` |
| `artist_id` | `uuid` | NOT NULL, FK → `artists.id` |
| `role` | `text` | NOT NULL, `CHECK (role IN ('primary', 'featured'))` |

PK `(recording_id, artist_id, role)`.

Индекс по `artist_id` нужен матчеру «normalized artist + title».

## release_tracks

Связь Recording и Release. Одна Recording может стоять на нескольких Release.

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `release_id` | `uuid` | NOT NULL, FK → `releases.id` |
| `recording_id` | `uuid` | NOT NULL, FK → `recordings.id` |
| `disc_number` | `integer` | NOT NULL, `CHECK (disc_number >= 1)` |
| `track_number` | `integer` | NOT NULL, `CHECK (track_number >= 1)` |
| `title_override` | `text` | NULL, если задан — длина > 0 |

Ограничения:

- PK не нужен отдельно: уникальность задают два ключа ниже.
- `UNIQUE (release_id, disc_number, track_number)` — одна позиция на релизе.
- `UNIQUE (release_id, recording_id)` — та же запись не ставится на релиз дважды.

Оба ограничения нужны. Первое ловит две разные записи на одном номере. Второе ловит одну запись, поставленную на два номера одного релиза. Повтор той же Recording на другом `release_id` разрешён.

Индекс по `recording_id` нужен, чтобы найти релизы, где запись уже стоит.

Порядок на странице релиза: `disc_number, track_number`. Отображаемый заголовок: `COALESCE(title_override, recordings.title)`.

## External IDs

Три таблицы, не одна полиморфная.

Общая форма колонок:

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `artist_id` / `release_id` / `recording_id` | `uuid` | NOT NULL, FK на свою сущность |
| `provider` | `text` | NOT NULL, длина > 0 |
| `external_id` | `text` | NOT NULL, длина > 0 |
| `last_synced_at` | `timestamptz` | NOT NULL |

Имена таблиц: `artist_external_ids`, `release_external_ids`, `recording_external_ids`.

Ограничения каждой:

- `UNIQUE (provider, external_id)` — главный барьер от дублей и от гонки двух импортов;
- `UNIQUE (artist_id, provider)` и аналоги для release и recording — у сущности не больше одного id на provider;
- индекс по FK-колонке, если он не покрыт уникальным ключом, начинающимся с этой колонки. `UNIQUE (entity_id, provider)` этот поиск покрывает.

Импорт пишет external ID в той же транзакции, что и сущность. Сырой JSON provider не хранится. `last_synced_at` обновляется при повторном импорте.

## audio_assets

Один playable-файл на Recording. Таблицы `audio_variants` нет.

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `id` | `uuid` | PK |
| `recording_id` | `uuid` | NOT NULL, `UNIQUE` |
| `source_provider` | `text` | NOT NULL, длина > 0 |
| `source_reference` | `text` | NULL. Для local — имя файла |
| `generation` | `integer` | NOT NULL, default 0, `CHECK (generation >= 0)` |
| `original_object_key` | `text` | NULL |
| `playable_object_key` | `text` | NULL |
| `codec` | `text` | NULL. Кодек playable, на MVP `aac` |
| `container` | `text` | NULL. На MVP `mp4` |
| `bitrate` | `integer` | NULL, бит/с playable |
| `sample_rate` | `integer` | NULL, Гц |
| `channels` | `integer` | NULL |
| `duration_ms` | `integer` | NULL, `CHECK (>= 0)` |
| `size_bytes` | `bigint` | NULL, размер playable |
| `checksum` | `text` | NULL, SHA-256 hex original |
| `status` | `text` | NOT NULL |
| `last_error` | `text` | NULL |
| `created_at` | `timestamptz` | NOT NULL, default `now()` |
| `updated_at` | `timestamptz` | NOT NULL |

```sql
CHECK (status IN ('PENDING', 'ACQUIRING', 'PROCESSING', 'READY', 'FAILED'))
```

`UNIQUE (recording_id)` заменяет отдельный индекс и не даёт завести второй asset на ту же запись.

Смысл полей, который легко перепутать:

- `checksum` — SHA-256 байтов original до транскода;
- `size_bytes`, кодек, контейнер, битрейт, частота, каналы, `duration_ms` — про playable-объект;
- `playable_object_key` публикуется в строке только вместе со статусом `READY`.

Жизненный цикл принадлежит этой таблице. Catalog его не копирует.

Повторная обработка с тем же `checksum` и `READY` ничего не меняет. Новая попытка увеличивает `generation` и пишет новые ключи. Условный `UPDATE ... WHERE generation = $mine` не даёт старой попытке затереть статус новой.

### Ссылка на Recording

Логически `audio_assets.recording_id` указывает на `recordings.id`. FK нет, пока не подтверждён вариант из [architecture.md](architecture.md):

```sql
ALTER TABLE audio_assets
    ADD CONSTRAINT audio_assets_recording_id_fkey
    FOREIGN KEY (recording_id) REFERENCES recordings (id)
    ON DELETE RESTRICT;
```

До этого приложение не удаляет Recording в MVP вообще, так что расхождение почти не проявляется. Ограничение всё равно лучше включить в первую миграцию audio, если решение будет принято до неё. Таблица `audio_assets` появляется в Phase 6; колонки Catalog — в Phase 2. FK можно добавить миграцией Phase 6, не переписывая Phase 2.

Удаление Recording на MVP не входит в API.

## import_jobs

| Колонка | Тип | Ограничения |
| --- | --- | --- |
| `id` | `uuid` | PK |
| `type` | `text` | NOT NULL, на MVP только `artist_import` |
| `status` | `text` | NOT NULL |
| `provider` | `text` | NOT NULL |
| `subject_external_id` | `text` | NOT NULL |
| `payload` | `jsonb` | NOT NULL, default `{}` |
| `progress` | `jsonb` | NOT NULL, default `{}` |
| `attempt` | `integer` | NOT NULL, default 0, `CHECK (attempt >= 0)` |
| `max_attempts` | `integer` | NOT NULL, default 5, `CHECK (max_attempts >= 1)` |
| `next_attempt_at` | `timestamptz` | NOT NULL, default `now()` |
| `locked_at` | `timestamptz` | NULL |
| `locked_by` | `uuid` | NULL |
| `last_error` | `text` | NULL |
| `created_at` | `timestamptz` | NOT NULL, default `now()` |
| `started_at` | `timestamptz` | NULL |
| `finished_at` | `timestamptz` | NULL |

```sql
CHECK (status IN ('QUEUED', 'RUNNING', 'COMPLETED', 'PARTIALLY_COMPLETED', 'FAILED'))
CHECK (type IN ('artist_import'))
CHECK (attempt <= max_attempts)
```

`provider` и `subject_external_id` — колонки, а не поля внутри `payload`. По ним ставится частичный уникальный индекс:

```sql
CREATE UNIQUE INDEX import_jobs_one_active_subject
    ON import_jobs (type, provider, subject_external_id)
    WHERE status IN ('QUEUED', 'RUNNING');
```

Два активных импорта одного артиста невозможны. После завершения новый job допустим.

Индекс claim:

```sql
CREATE INDEX import_jobs_claim_idx
    ON import_jobs (next_attempt_at, created_at)
    WHERE status = 'QUEUED';
```

Индекс recovery:

```sql
CREATE INDEX import_jobs_running_lease_idx
    ON import_jobs (locked_at)
    WHERE status = 'RUNNING';
```

`payload` на MVP пустой объект или мелкие параметры. Вход job — `provider` + `subject_external_id`.

`progress` — выход worker, не вход:

```json
{
  "artist_id": "uuid",
  "releases_total": 2,
  "releases_done": 2,
  "recordings_total": 3,
  "recordings_imported": 3,
  "audio_ready": 2,
  "audio_failed": 1
}
```

Поля появляются по мере продвижения. Клиент терпит отсутствие ключа.

Статусы:

| Статус | Когда |
| --- | --- |
| `QUEUED` | Ждёт worker. Сюда же возвращается lease expiry и мягкая остановка |
| `RUNNING` | Lock удерживается |
| `COMPLETED` | Каталог записан, все нужные asset в `READY` либо audio для записи не требовался |
| `PARTIALLY_COMPLETED` | Каталог записан, хотя бы один asset `FAILED` |
| `FAILED` | Каталог не доведён, либо исчерпаны попытки, либо постоянная ошибка provider |

`PARTIALLY_COMPLETED` — конечное состояние до ручного retry. Оно не прячет успех каталога внутри общего `FAILED`.

## Связи

```text
artists 1───* artist_external_ids
artists *───* releases          через release_artists
artists *───* recordings        через recording_artists
releases 1──* release_external_ids
releases *──* recordings        через release_tracks
recordings 1──* recording_external_ids
recordings 1──1 audio_assets    логически, UNIQUE(recording_id)
```

Удаление на MVP не реализуется. FK Catalog поэтому достаточно объявить без каскада (`ON DELETE RESTRICT` по умолчанию у PostgreSQL — `NO ACTION`). Явно писать `ON DELETE CASCADE` не нужно.

## Что проверяет модель Recording / Release

Тестовый каталог `FakeCatalogProvider` (контракт данных, реализация в Phase 4):

| Сущность | provider | external_id | Содержимое |
| --- | --- | --- | --- |
| Artist | `fake` | `test-artist` | Test Artist |
| Release | `fake` | `first-album` | First Album, `album` |
| Release | `fake` | `first-album-deluxe` | First Album Deluxe, `album` |
| Recording | `fake` | `track-one` | Track One, ISRC `USTST2600001`, 180000 мс |
| Recording | `fake` | `track-two` | Track Two, без ISRC, 200000 мс |
| Recording | `fake` | `track-three` | Track Three, без ISRC, 150000 мс |

Состав:

- First Album: disc 1, Track One (`1`), Track Two (`2`).
- First Album Deluxe: disc 1, Track One (`1`), Track Two (`2`), Track Three (`3`).

Track One и Track Two — одни и те же строки `recordings` на двух релизах. Deluxe не копирует запись.

Primary artist всех трёх Recording и обоих Release — Test Artist.

Файлы `LocalAudioProvider`: `{LOCAL_AUDIO_DIR}/track-one.wav`, `track-two.wav`, `track-three.wav`.
