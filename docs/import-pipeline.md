# Import pipeline

Импорт асинхронный. `POST /api/v1/admin/catalog/artists/import` вставляет `import_jobs` и возвращает его. Worker делает остальное.

На MVP pipeline целиком внутри одного job `artist_import`. Отдельные audio-jobs не создаются.

## Последовательность

```text
Admin UI
  -> GET admin search
  -> CatalogProvider.SearchArtists
  -> Admin выбирает provider + external_id
  -> POST import
  -> ImportJob QUEUED
  -> Worker claim (SKIP LOCKED)
  -> CatalogProvider: GetArtist, GetArtistReleases, GetReleaseDetails
  -> Identity Resolver
  -> PostgreSQL: Artist, Recordings, Releases
  -> для каждой Recording:
       AudioProvider
       -> временный файл
       -> validation
       -> ffprobe
       -> SHA-256
       -> FFmpeg AAC-LC faststart
       -> Put original и playable
       -> AudioAsset READY
  -> ImportJob COMPLETED или PARTIALLY_COMPLETED
```

Дальше пользователь открывает страницы уже из PostgreSQL. Play подписывает URL, браузер читает Object Storage.

## Контракты provider

Интерфейсы маленькие. Общего «бог-интерфейса» нет.

Catalog:

- `SearchArtists(ctx, query, limit) ([]ArtistCandidate, error)`
- `GetArtist(ctx, externalID) (Artist, error)`
- `GetArtistReleases(ctx, externalID) ([]ReleaseSummary, error)`
- `GetReleaseDetails(ctx, externalID) (Release, error)`

`Release` внутри деталей уже содержит треки и стабильные `external_id` записей. Отдельный `GetRecording` на MVP не нужен.

Audio:

- `Open(ctx, recordingExternalID) (source io.ReadCloser, reference string, error)`

`LocalAudioProvider` открывает `{LOCAL_AUDIO_DIR}/{externalID}.wav`.

Бизнес-логика зависит от этих интерфейсов. Первые реализации — `FakeCatalogProvider` и `LocalAudioProvider`. Ошибок вида «контент защищён, обойди ограничение» в интерфейсе нет.

Фейковый каталог описан в [data-model.md](data-model.md). Оба провайдера в процессе, без сети.

## Claim, lease, fencing

Идентификатор worker — UUID на время жизни процесса.

1. Recovery просроченных `RUNNING` (lease 2 минуты).
2. Один `UPDATE ... FOR UPDATE SKIP LOCKED`, attempt увеличивается, статус `RUNNING`, lock пишется.
3. Каждые 30 секунд heartbeat обновляет `locked_at`, только если `locked_by` и `attempt` ещё наши.
4. Любая запись результата job и финальный статус asset проходят с тем же условием.
5. Ноль обновлённых строк — lease потерян. Worker удаляет свой temp и выходит из job. Объекты чужого `generation` не трогает.

Время lease считается в PostgreSQL. Подробный SQL — в [architecture.md](architecture.md).

`AUDIO_PROCESS_TIMEOUT` = 10 минут на одну Recording. По таймауту процесс FFmpeg убивается, asset получает ошибку, job продолжает остальные записи.

## Границы записи в БД

Provider читается до транзакций.

1. Транзакция: Artist + external ID.
2. Транзакция на каждую Recording: запись, артисты, external ID. Resolver внутри этой транзакции.
3. Транзакция на каждый Release: релиз, артисты, треки, external ID.

Между шагами job обновляет `progress` условным `UPDATE`. После шага 3 каталог уже виден в API, даже если audio ещё идёт.

Audio одной Recording:

1. Условное увеличение `generation`, статус `ACQUIRING`, если это не пропуск.
2. Чтение источника во временный файл `{AUDIO_TEMP_DIR}/{job_id}/{recording_id}/`.
3. Проверки: файл не пустой, размер не больше `AUDIO_MAX_BYTES` (200 МиБ), `ffprobe` видит аудиопоток и длительность больше нуля.
4. SHA-256 original.
5. Если текущий asset уже `READY` с тем же checksum — generation откатывать не нужно. Этот путь проверяется до увеличения generation.
6. FFmpeg, статус `PROCESSING`.
7. Put original, затем Put playable в префикс `g{generation}`.
8. Одна транзакция: условно обновить asset (`generation` совпал) и условно обновить прогресс job (lock и `attempt` совпали). Если любое условие не сработало, транзакция откатывается, `READY` не публикуется.

Временный каталог удаляется через `defer` при успехе, ошибке и потере lease.

Пропуск шага 6–7 при совпавшем checksum — нормальный повтор, а не ошибка.

## Идемпотентность

Повторный `artist_import` того же `fake/test-artist`:

- не создаёт второго Artist, Release, Recording;
- Track One на обоих релизах остаётся одной строкой `recordings`;
- не создаёт второй `audio_assets` (`UNIQUE(recording_id)`);
- не затирает playable, если original не менялся;
- при новой попытке после сбоя пишет следующий `generation`, а не тот же ключ.

Гонка двух процессов на одном external ID упирается в `UNIQUE (provider, external_id)`. Проигравшая транзакция перечитывает победившую строку и работает с ней.

Активный дубль job не вставляется: частичный уникальный индекс, API возвращает уже существующий job.

## Ошибки и retry

Ошибки делятся в коде типом, не сравнением текста.

Постоянные, без повторных attempt job:

- артист не найден у provider → job `FAILED`;
- ответ provider нарушает контракт (нет external ID записи) → job `FAILED`.

Временные, attempt job растёт, пока не кончится `max_attempts` (5):

- таймаут и сетевой сбой provider, если provider когда-нибудь станет сетевым;
- недоступна PostgreSQL на середине работы;
- недоступен Object Storage;
- таймаут FFmpeg.

После временной ошибки lock снимается, статус `QUEUED`, `next_attempt_at = now() + min(2^attempt секунд, 5 минут)`.

Ошибка конкретного файла не валит каталог и не валит остальные записи:

- нет файла у LocalAudioProvider;
- validation не прошла;
- ffprobe не видит аудио;
- FFmpeg вернул ошибку декодирования.

Такая Recording получает asset `FAILED` и `last_error`. Остальные продолжаются. Если каталог записан и есть хотя бы один `FAILED`, job завершается `PARTIALLY_COMPLETED`. Если все дошли до `READY`, job `COMPLETED`.

`PARTIALLY_COMPLETED` сам в очередь не возвращается. Админский retry обнуляет attempt и ставит `QUEUED`. Повтор пропускает `READY` с тем же checksum и заново берёт `FAILED`.

Исчерпание attempt на временной ошибке каталога даёт `FAILED`. Каталог, уже закоммиченный короткими транзакциями, остаётся.

## Падение worker

| Событие | Результат |
| --- | --- |
| `SIGTERM` во время job | Temp удалён, job возвращён в `QUEUED`, attempt уменьшен, lock снят |
| `SIGKILL`, OOM, смерть контейнера | Lock жив до 2 минут, heartbeat замолкает, recovery возвращает job в `QUEUED`. Attempt уже учтён |
| Смерть во время FFmpeg | Temp умрёт вместе с контейнером. Объект либо не дописан, либо лежит в префиксе старого generation и не указан как `READY` |
| Потеря lease и поздний Put | Put идёт в свой generation. Финальный `UPDATE` не совпадёт по generation или lock и не станет `READY` |
| Два worker | `SKIP LOCKED` отдаёт разные jobs. Один subject не бывает в двух активных jobs |

Рестарт контейнера не требует ручной починки строки. Достаточно, чтобы какой-нибудь worker выполнял recovery и claim.

## Прогресс

`progress` обновляется после каждого агрегата и после каждой Recording. Поля перечислены в [data-model.md](data-model.md). Admin UI читает job поллингом `GET /api/v1/admin/import-jobs/{id}`. Отдельного канала событий нет.

## Что сознательно не делается

- транзакция на весь каталог артиста;
- хранение сырого JSON provider;
- автоматическое слияние артистов по похожему имени;
- автоматическое слияние Recording при неоднозначном ISRC;
- удаление объектов предыдущих generation;
- вторая очередь для audio;
- публикация доменных событий.
