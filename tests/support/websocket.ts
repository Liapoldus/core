import { createHash } from "node:crypto";
import { connect } from "node:net";
import type { Socket } from "node:net";
import { createServer as createHTTPServer } from "node:http";

export const WS_GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";

export interface WebSocketFrame {
  opcode: number;
  payload: Buffer;
}

export function encodeFrame(fin: boolean, opcode: number, payload: Buffer | string, mask: boolean): Buffer {
  const body = typeof payload === "string" ? Buffer.from(payload, "utf8") : payload;
  if (body.length >= 126) {
    throw new Error("test frame codec does not support payloads of 126 bytes or more");
  }
  const header = Buffer.alloc(mask ? 6 : 2);
  header[0] = (fin ? 0x80 : 0x00) | (opcode & 0x0f);
  if (mask) {
    header[1] = 0x80 | body.length;
    for (let i = 0; i < 4; i += 1) {
      header[2 + i] = Math.floor(Math.random() * 256);
    }
    const key = header.subarray(2, 6);
    const masked = Buffer.alloc(body.length);
    for (let i = 0; i < body.length; i += 1) {
      masked[i] = body[i] ^ key[i % 4];
    }
    return Buffer.concat([header, masked]);
  }
  header[1] = body.length;
  return Buffer.concat([header, body]);
}

export function decodeFrames(data: Buffer): WebSocketFrame[] {
  const frames: WebSocketFrame[] = [];
  let offset = 0;
  while (offset + 2 <= data.length) {
    const b0 = data[offset];
    const opcode = b0 & 0x0f;
    const b1 = data[offset + 1];
    const length = b1 & 0x7f;
    const masked = (b1 & 0x80) !== 0;
    if (length === 127) {
      throw new Error("test frame codec does not support 64-bit lengths");
    }
    const maskOffset = masked ? 4 : 0;
    const payloadStart = offset + 2 + maskOffset;
    if (payloadStart + length > data.length) {
      break;
    }
    const mask = masked ? data.subarray(offset + 2, offset + 6) : null;
    const raw = data.subarray(payloadStart, payloadStart + length);
    if (mask) {
      const unmasked = Buffer.alloc(raw.length);
      for (let i = 0; i < raw.length; i += 1) {
        unmasked[i] = raw[i] ^ mask[i % 4];
      }
      frames.push({ opcode, payload: unmasked });
    } else {
      frames.push({ opcode, payload: Buffer.from(raw) });
    }
    offset = payloadStart + length;
  }
  return frames;
}

export interface EchoWebSocketServer {
  url: string;
  stop: () => Promise<void>;
}

export async function startEchoWebSocketServer(): Promise<EchoWebSocketServer> {
  const server = createHTTPServer((_request, response) => {
    response.writeHead(426, { connection: "close" });
    response.end();
  });
  const upgraded = new Set<Socket>();

  server.on("upgrade", (request, socket: Socket, head: Buffer) => {
    upgraded.add(socket);
    socket.on("close", () => upgraded.delete(socket));
    socket.on("error", () => upgraded.delete(socket));
    const key = request.headers["sec-websocket-key"];
    if (typeof key !== "string") {
      socket.destroy();
      return;
    }
    const accept = createHash("sha1").update(`${key}${WS_GUID}`).digest("base64");
    socket.write(
      `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );

    let pending = Buffer.alloc(0);
    const handle = (chunk: Buffer) => {
      pending = Buffer.concat([pending, chunk]);
      for (const frame of decodeFrames(pending)) {
        if (frame.opcode === 0x8) {
          socket.write(encodeFrame(true, 0x8, frame.payload, false));
          socket.end();
          return;
        }
        if (frame.opcode === 0x9) {
          socket.write(encodeFrame(true, 0xa, frame.payload, false));
          continue;
        }
        socket.write(encodeFrame(true, frame.opcode, frame.payload, false));
      }
      pending = Buffer.alloc(0);
    };

    if (head.length > 0) {
      handle(head);
    }
    socket.on("data", handle);
    socket.on("error", () => undefined);
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
    url: `http://${base}`,
    stop: () => {
      for (const socket of upgraded) {
        socket.destroy();
      }
      upgraded.clear();
      return new Promise<void>((resolve, reject) => {
        server.close((error) => (error ? reject(error) : resolve()));
      });
    },
  };
}

export async function websocketEcho(address: string, path: string, message: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const host = address.lastIndexOf(":") >= 0 ? address.slice(0, address.lastIndexOf(":")) : address;
    const port = Number(address.slice(address.lastIndexOf(":") + 1));
    const socket = connect(port, host);
    const key = createHash("sha1").update(`${Date.now()}${Math.random()}`).digest("base64");

    let buffer = Buffer.alloc(0);
    let handshakeDone = false;
    let finished = false;

    const finish = (error: Error | null, value?: string) => {
      if (finished) {
        return;
      }
      finished = true;
      clearTimeout(timeout);
      socket.destroy();
      if (error) {
        reject(error);
        return;
      }
      resolve(value as string);
    };

    const timeout = setTimeout(() => finish(new Error("websocket echo timed out")), 10000);

    socket.on("error", (error) => finish(error));
    socket.on("close", () => finish(new Error("websocket closed before reply")));

    socket.on("data", (chunk: Buffer) => {
      buffer = Buffer.concat([buffer, chunk]);
      if (!handshakeDone) {
        const marker = buffer.indexOf("\r\n\r\n");
        if (marker < 0) {
          return;
        }
        const header = buffer.subarray(0, marker).toString("utf8");
        const status = header.split("\r\n")[0];
        handshakeDone = true;
        buffer = buffer.subarray(marker + 4);
        if (!status.startsWith("HTTP/1.1 101")) {
          finish(new Error(`expected 101 upgrade, got: ${status}`));
          return;
        }
        socket.write(encodeFrame(true, 0x1, message, true));
      }
      let frames: WebSocketFrame[];
      try {
        frames = decodeFrames(buffer);
      } catch (error) {
        finish(error instanceof Error ? error : new Error(String(error)));
        return;
      }
      const reply = frames.find((frame) => frame.opcode === 0x1 || frame.opcode === 0x2);
      if (reply) {
        finish(null, reply.payload.toString("utf8"));
      }
    });

    socket.once("connect", () => {
      socket.write(
        [
          `GET ${path} HTTP/1.1`,
          `Host: ${address}`,
          "Upgrade: websocket",
          "Connection: Upgrade",
          `Sec-WebSocket-Key: ${key}`,
          "Sec-WebSocket-Version: 13",
          "",
          "",
        ].join("\r\n"),
      );
    });
  });
}