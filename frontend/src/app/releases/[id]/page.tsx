"use client";

import { useEffect, useRef, useState } from "react";
import { useParams } from "next/navigation";
import { api, formatDuration, Playback, Release, RequestError, Track } from "../../../lib/api";

export default function ReleasePage() {
  const params = useParams<{ id: string }>();
  const audioRef = useRef<HTMLAudioElement>(null);
  const [release, setRelease] = useState<Release | null>(null);
  const [current, setCurrent] = useState<Track | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    void api<Release>(`/api/v1/releases/${params.id}`)
      .then(setRelease)
      .catch((err: unknown) => {
        setError(err instanceof RequestError ? err.message : "Релиз не загружен");
      });
  }, [params.id]);

  async function play(track: Track) {
    setError("");
    setCurrent(track);
    try {
      const playback = await api<Playback>(`/api/v1/recordings/${track.recording_id}/play`);
      const audio = audioRef.current;
      if (!audio) {
        return;
      }
      audio.src = playback.url;
      await audio.play();
    } catch (err) {
      setError(err instanceof RequestError ? err.message : "Не удалось начать воспроизведение");
    }
  }

  return (
    <main>
      <h1>{release?.title ?? "Релиз"}</h1>
      {release ? (
        <p>
          {release.release_type}
          {release.release_date ? `, ${release.release_date}` : ""}
          {release.artists.length > 0 ? `. ${release.artists.map((artist) => artist.name).join(", ")}` : ""}
        </p>
      ) : null}
      {error ? <p className="error">{error}</p> : null}
      <ol>
        {release?.tracks.map((track) => (
          <li key={track.recording_id}>
            <span>
              {track.disc_number}.{track.track_number} {track.title} ({formatDuration(track.duration_ms)})
            </span>
            <button type="button" disabled={!track.playback_available} onClick={() => void play(track)}>
              Play
            </button>
          </li>
        ))}
      </ol>
      <section>
        <h2>Плеер</h2>
        <p>{current ? current.title : "Трек не выбран"}</p>
        <audio ref={audioRef} controls />
      </section>
    </main>
  );
}
