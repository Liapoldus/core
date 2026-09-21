import { createServer } from "node:http";
import type { Socket } from "node:net";
import type { Readable } from "node:stream";

export interface UpstreamHits {
  requests: number;
  paths: string[];
  headers: Array<Record<string, string | string[] | undefined>>;
  bodies: string[];
}

export interface UpstreamProbe {
  address: string;
  hits: () => UpstreamHits;
  stop: () => Promise<void>;
}

export async function startUpstream(): Promise<UpstreamProbe> {
  const requests: UpstreamHits = {
    requests: 0,
    paths: [],
    headers: [],
    bodies: [],
  };

  const server = createServer((request, response) => {
    requests.requests += 1;
    requests.paths.push(request.url ?? "");
    requests.headers.push(request.headers);
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => {
      requests.bodies.push(Buffer.concat(chunks).toString("utf8"));
      response.writeHead(200, { "content-type": "text/plain" });
      response.end("upstream");
    });
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  const address = server.address();
  if (typeof address === "string" || address === null) {
    throw new Error("expected TCP address");
  }
  const base = `127.0.0.1:${address.port}`;

  return {
    address: base,
    hits: () => ({ ...requests, paths: [...requests.paths], headers: [...requests.headers], bodies: [...requests.bodies] }),
    stop: () =>
      new Promise<void>((resolve, reject) => {
        server.close((error) => (error ? reject(error) : resolve()));
      }),
  };
}

export async function startRefusingUpstream(): Promise<{
  address: string;
  connections: () => number;
  stop: () => Promise<void>;
}> {
  const sockets: Socket[] = [];
  let connections = 0;
  const server = createServer(() => undefined);

  server.on("connection", (socket: Socket) => {
    connections += 1;
    sockets.push(socket);
    socket.destroy();
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });

  const address = server.address();
  if (typeof address === "string" || address === null) {
    throw new Error("expected TCP address");
  }
  const base = `127.0.0.1:${address.port}`;

  return {
    address: base,
    connections: () => connections,
    stop: () =>
      new Promise<void>((resolve, reject) => {
        for (const socket of sockets) {
          socket.destroy();
        }
        server.close((error) => (error ? reject(error) : resolve()));
      }),
  };
}

export async function readBody(stream: Readable): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of stream) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
  }
  return Buffer.concat(chunks).toString("utf8");
}