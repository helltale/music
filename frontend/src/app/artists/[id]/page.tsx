"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { api, Artist, Page, ReleaseItem, RequestError } from "../../../lib/api";

export default function ArtistPage() {
  const params = useParams<{ id: string }>();
  const [artist, setArtist] = useState<Artist | null>(null);
  const [releases, setReleases] = useState<Page<ReleaseItem> | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const id = params.id;
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
  }, [params.id]);

  return (
    <main>
      <h1>{artist?.name ?? "Артист"}</h1>
      {artist?.description ? <p>{artist.description}</p> : null}
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
