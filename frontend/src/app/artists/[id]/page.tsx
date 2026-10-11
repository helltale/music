"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { api, Artist, ImportJob, Page, ReleaseItem, RequestError } from "../../../lib/api";

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
  }

  useEffect(() => {
    load(params.id);
  }, [params.id]);

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
