"use client";

import { FormEvent, useEffect, useState } from "react";
import { api, Candidate, ImportJob, Page, RequestError } from "../../lib/api";

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

export default function AdminPage() {
  const [query, setQuery] = useState("");
  const [candidates, setCandidates] = useState<Page<Candidate> | null>(null);
  const [job, setJob] = useState<ImportJob | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!job || finished(job.status)) {
      return;
    }
    const timer = setInterval(() => {
      void api<ImportJob>(`/api/v1/admin/import-jobs/${job.id}`)
        .then(setJob)
        .catch((err: unknown) => {
          setError(err instanceof RequestError ? err.message : "Не удалось обновить job");
        });
    }, 1000);
    return () => clearInterval(timer);
  }, [job]);

  async function onSearch(event: FormEvent) {
    event.preventDefault();
    setError("");
    setCandidates(null);
    try {
      const search = new URLSearchParams({ q: query.trim() });
      setCandidates(await api<Page<Candidate>>(`/api/v1/admin/catalog/artists/search?${search}`));
    } catch (err) {
      setError(err instanceof RequestError ? err.message : "Поиск не выполнен");
    }
  }

  async function onImport(candidate: Candidate) {
    setError("");
    setBusy(true);
    try {
      const created = await api<ImportJob>("/api/v1/admin/catalog/artists/import", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ provider: candidate.provider, external_id: candidate.external_id }),
      });
      setJob(created);
    } catch (err) {
      setError(err instanceof RequestError ? err.message : "Импорт не создан");
    } finally {
      setBusy(false);
    }
  }

  const progress = job?.progress ?? {};

  return (
    <main>
      <h1>Импорт артиста</h1>
      <form onSubmit={onSearch}>
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Имя у провайдера"
          aria-label="Имя у провайдера"
        />
        <button type="submit">Найти</button>
      </form>
      {error ? <p className="error">{error}</p> : null}
      {candidates && candidates.items.length === 0 ? <p>Никого не найдено.</p> : null}
      <ul>
        {candidates?.items.map((candidate) => (
          <li key={`${candidate.provider}:${candidate.external_id}`}>
            <span>
              {candidate.name} · {candidate.provider}
            </span>
            <button type="button" disabled={busy} onClick={() => void onImport(candidate)}>
              Импорт
            </button>
          </li>
        ))}
      </ul>
      {job ? (
        <section>
          <h2>Задача</h2>
          <p>
            {labels[job.status] ?? job.status}
            {job.last_error ? `. ${job.last_error}` : ""}
          </p>
          <p>
            Релизы {progress.releases_done ?? 0}/{progress.releases_total ?? 0}. Записи{" "}
            {progress.recordings_imported ?? 0}/{progress.recordings_total ?? 0}. Аудио готово{" "}
            {progress.audio_ready ?? 0}, ошибок {progress.audio_failed ?? 0}.
          </p>
          {progress.artist_id ? <a href={`/artists/${progress.artist_id}`}>Открыть артиста</a> : null}
        </section>
      ) : null}
    </main>
  );
}
