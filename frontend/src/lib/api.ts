export type APIError = { error: { code: string; message: string } };

export type ArtistItem = { id: string; name: string };

export type Artist = ArtistItem & {
  description: string | null;
  image_object_key: string | null;
};

export type ReleaseItem = {
  id: string;
  title: string;
  release_type: string;
  release_date: string | null;
  cover_object_key: string | null;
};

export type Role = { id: string; name: string; role: string };

export type Track = {
  disc_number: number;
  track_number: number;
  title: string;
  recording_id: string;
  duration_ms: number | null;
  playback_available: boolean;
};

export type Release = ReleaseItem & {
  artists: Role[];
  tracks: Track[];
};

export type Page<T> = { items: T[]; limit: number; offset: number; total: number };

export type Candidate = { provider: string; external_id: string; name: string };

export type ImportJob = {
  id: string;
  status: string;
  provider: string;
  external_id: string;
  attempt: number;
  max_attempts: number;
  progress: {
    artist_id?: string;
    releases_total?: number;
    releases_done?: number;
    recordings_total?: number;
    recordings_imported?: number;
    audio_ready?: number;
    audio_failed?: number;
  };
  last_error: string | null;
};

export type Playback = {
  recording_id: string;
  audio_asset_id: string;
  url: string;
  expires_at: string;
  content_type: string;
  duration_ms: number | null;
};

export class RequestError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { Accept: "application/json", ...init?.headers },
  });
  const body = (await response.json()) as T | APIError;
  if (!response.ok) {
    const message = isError(body) ? body.error.message : "Request failed";
    throw new RequestError(message, response.status);
  }
  return body as T;
}

function isError(body: unknown): body is APIError {
  return typeof body === "object" && body !== null && "error" in body;
}

export function formatDuration(ms: number | null): string {
  if (ms == null) {
    return "—";
  }
  const total = Math.round(ms / 1000);
  const minutes = Math.floor(total / 60);
  const seconds = total % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
}
