"use client";

import { FormEvent, useEffect, useState } from "react";
import { api, ArtistItem, Page, RequestError } from "../lib/api";

export default function HomePage() {
  const [query, setQuery] = useState("");
  const [page, setPage] = useState<Page<ArtistItem> | null>(null);
  const [error, setError] = useState("");

  async function load(q: string) {
    setError("");
    try {
      const search = new URLSearchParams({ limit: "20" });
      if (q.trim() !== "") {
        search.set("q", q.trim());
      }
      setPage(await api<Page<ArtistItem>>(`/api/v1/artists?${search}`));
    } catch (err) {
      setPage(null);
      setError(err instanceof RequestError ? err.message : "Не удалось загрузить каталог");
    }
  }

  useEffect(() => {
    void load("");
  }, []);

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    void load(query);
  }

  return (
    <main>
      <h1>Каталог</h1>
      <form onSubmit={onSubmit}>
        <input
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Имя артиста"
          aria-label="Имя артиста"
        />
        <button type="submit">Найти</button>
      </form>
      {error ? <p className="error">{error}</p> : null}
      {page && page.items.length === 0 ? <p>Артистов нет.</p> : null}
      <ul>
        {page?.items.map((artist) => (
          <li key={artist.id}>
            <a href={`/artists/${artist.id}`}>{artist.name}</a>
          </li>
        ))}
      </ul>
    </main>
  );
}
