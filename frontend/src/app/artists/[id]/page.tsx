"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { api, Artist, ArtistSync, ImportJob, Page, ReleaseItem, RequestError } from "../../../lib/api";

const labels: Record<string, string> = {
  QUEUED: "Ожидает",
  RUNNING: "Выполняется",
  COMPLETED: "Готово",
  PARTIALLY_COMPLETED: "Частично готово",
  FAILED: "Ошибка",
};

function finished(status: string) {
  return status === "COMPLETED" || status === "PARTIALLY_COMPLETED" || status === "FAILED";
}

export default function ArtistPage() {
  const params = useParams<{ id: string }>();
  const [artist, setArtist] = useState<Artist | null>(null);
  const [releases, setReleases] = useState<Page<ReleaseItem> | null>(null);
  const [sync, setSync] = useState<ArtistSync | null>(null);
  const [job, setJob] = useState<ImportJob | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function load(id: string) {
    void api<Artist>(`/api/v1/artists/${id}`)
      .then(setArtist)
      .catch((err: unknown) => {
        setError(err instanceof RequestError ? err.message : "Артист не загружен");
      });
    void api<Page<ReleaseItem>>(`/api/v1/artists/${id}/releases`)
      .then(setReleases)
      .catch((err: unknown) => {
        setError(err instanceof RequestError ? err.message : "Релизы не загружены");
      });
    void api<ArtistSync>(`/api/v1/admin/artists/${id}`)
      .then(setSync)
      .catch((err: unknown) => {
        setError(err instanceof RequestError ? err.message : "Состояние синхронизации не загружено");
      });
  }

  useEffect(() => {
    load(params.id);
  }, [params.id]);

  useEffect(() => {
    if (!sync?.sync_enabled) {
      return;
    }
    const timer = setInterval(() => {
      void api<ArtistSync>(`/api/v1/admin/artists/${params.id}`)
        .then(setSync)
        .catch((err: unknown) => {
          setError(err instanceof RequestError ? err.message : "Состояние синхронизации не загружено");
        });
    }, 2000);
    return () => clearInterval(timer);
  }, [sync?.sync_enabled, params.id]);

  useEffect(() => {
    if (!job || finished(job.status)) {
      return;
    }
    const timer = setInterval(() => {
      void api<ImportJob>(`/api/v1/admin/import-jobs/${job.id}`)
        .then((next) => {
          setJob(next);
          if (finished(next.status)) {
            load(params.id);
          }
        })
        .catch((err: unknown) => {
          setError(err instanceof RequestError ? err.message : "Не удалось обновить синхронизацию");
        });
    }, 1000);
    return () => clearInterval(timer);
  }, [job, params.id]);

  async function onToggle(enabled: boolean) {
    setError("");
    setBusy(true);
    try {
      setSync(
        await api<ArtistSync>(`/api/v1/admin/artists/${params.id}`, {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sync_enabled: enabled }),
        }),
      );
    } catch (err) {
      setError(err instanceof RequestError ? err.message : "Автосинхронизация не изменена");
    } finally {
      setBusy(false);
    }
  }

  async function onSync() {
    setError("");
    setBusy(true);
    try {
      setJob(await api<ImportJob>(`/api/v1/admin/artists/${params.id}/sync`, { method: "POST" }));
    } catch (err) {
      setError(err instanceof RequestError ? err.message : "Синхронизация не запущена");
    } finally {
      setBusy(false);
    }
  }

  const progress = job?.progress ?? {};

  return (
    <main>
      <h1>{artist?.name ?? "Артист"}</h1>
      {artist?.description ? <p>{artist.description}</p> : null}
      <button type="button" disabled={busy} onClick={() => void onSync()}>
        Синхронизировать
      </button>
      <label>
        <input
          type="checkbox"
          checked={sync?.sync_enabled ?? false}
          disabled={busy || sync === null}
          onChange={(event) => void onToggle(event.target.checked)}
        />
        Автоматически
      </label>
      {sync?.last_synced_at ? <p>Последняя синхронизация {new Date(sync.last_synced_at).toLocaleString()}</p> : null}
      {sync?.sync_enabled && sync.next_sync_at ? (
        <p>Следующая синхронизация {new Date(sync.next_sync_at).toLocaleString()}</p>
      ) : null}
      {job ? (
        <p>
          {labels[job.status] ?? job.status}
          {job.last_error ? `. ${job.last_error}` : ""}. Релизы {progress.releases_done ?? 0}/
          {progress.releases_total ?? 0}. Записи {progress.recordings_imported ?? 0}/
          {progress.recordings_total ?? 0}. Аудио готово {progress.audio_ready ?? 0}, ошибок{" "}
          {progress.audio_failed ?? 0}.
        </p>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
      {releases && releases.items.length === 0 ? <p>Релизов нет.</p> : null}
      <ul>
        {releases?.items.map((release) => (
          <li key={release.id}>
            <a href={`/releases/${release.id}`}>{release.title}</a>
            <span>
              {release.release_type}
              {release.release_date ? `, ${release.release_date}` : ""}
            </span>
          </li>
        ))}
      </ul>
    </main>
  );
}
