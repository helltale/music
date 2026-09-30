import { NextRequest, NextResponse } from "next/server";

export const dynamic = "force-dynamic";

const hopByHop = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailers",
  "transfer-encoding",
  "upgrade",
  "host",
  "content-length",
]);

type Context = { params: Promise<{ path: string[] }> };

async function proxy(request: NextRequest, context: Context): Promise<NextResponse> {
  const upstream = process.env.API_UPSTREAM;
  if (!upstream) {
    return NextResponse.json(
      { error: { code: "INTERNAL", message: "API upstream is not configured" } },
      { status: 500 },
    );
  }

  const { path } = await context.params;
  const target = new URL(request.url);
  const base = upstream.replace(/\/$/, "");
  const url = `${base}/api/v1/${path.join("/")}${target.search}`;

  const headers = new Headers();
  request.headers.forEach((value, key) => {
    if (!hopByHop.has(key.toLowerCase())) {
      headers.set(key, value);
    }
  });

  const init: RequestInit = {
    method: request.method,
    headers,
    redirect: "manual",
  };
  if (request.method !== "GET" && request.method !== "HEAD") {
    init.body = await request.arrayBuffer();
  }

  let response: Response;
  try {
    response = await fetch(url, init);
  } catch {
    return NextResponse.json(
      { error: { code: "UPSTREAM_UNAVAILABLE", message: "API is unavailable" } },
      { status: 502 },
    );
  }

  const outHeaders = new Headers();
  response.headers.forEach((value, key) => {
    if (!hopByHop.has(key.toLowerCase())) {
      outHeaders.set(key, value);
    }
  });
  return new NextResponse(response.body, { status: response.status, headers: outHeaders });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const PATCH = proxy;
export const DELETE = proxy;
export const HEAD = proxy;
