import {spawn} from "node:child_process";
import {pathToFileURL} from "node:url";

import {createMessageConnection} from "vscode-jsonrpc/node";

import {collect, decodeUTF8, errorText} from "./common.ts";

type LSPPosition = {
  line: number;
  character: number;
};

type LSPRange = {
  start: LSPPosition;
  end: LSPPosition;
};

export type LSPLocation = {
  uri: string;
  range: LSPRange;
};

type LSPQueryResult = {
  locations: LSPLocation[];
  stderr: string;
};

type LSPQueryOptions = {
  command: string;
  args: string[];
  workspace: string;
  path: string;
  languageID: string;
  source: string;
  position: LSPPosition;
  mode: "def" | "refs";
};

function position(value: unknown): LSPPosition | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  const {line, character} = value as {line?: unknown; character?: unknown};
  return Number.isSafeInteger(line) && (line as number) >= 0
    && Number.isSafeInteger(character) && (character as number) >= 0
    ? {line: line as number, character: character as number}
    : null;
}

function range(value: unknown): LSPRange | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  const start = position((value as {start?: unknown}).start);
  const end = position((value as {end?: unknown}).end);
  return start === null || end === null ? null : {start, end};
}

function location(value: unknown): LSPLocation | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  const candidate = value as {
    uri?: unknown;
    range?: unknown;
    targetUri?: unknown;
    targetRange?: unknown;
    targetSelectionRange?: unknown;
  };
  const uri = typeof candidate.uri === "string"
    ? candidate.uri
    : typeof candidate.targetUri === "string" ? candidate.targetUri : null;
  const selectedRange = candidate.targetSelectionRange ?? candidate.range ?? candidate.targetRange;
  const parsedRange = range(selectedRange);
  return uri === null || parsedRange === null ? null : {uri, range: parsedRange};
}

function locations(value: unknown, mode: "def" | "refs"): LSPLocation[] {
  if (value === null) {
    return [];
  }
  const values = Array.isArray(value) ? value : mode === "def" ? [value] : null;
  if (values === null) {
    throw new Error("invalid references response");
  }
  return values.map((value) => {
    const parsed = location(value);
    if (parsed === null) {
      throw new Error(`invalid ${mode === "def" ? "definition" : "references"} response`);
    }
    return parsed;
  });
}

function semanticStderr(stderr: string): string {
  return stderr
    .split(/(?<=\n)/u)
    .filter((line) => {
      const message = line.trim();
      return message !== "context canceled" && message !== "error handling method 'exit': EOF";
    })
    .join("");
}

function processFailure(command: string, error: Error): Error {
  return "code" in error && error.code === "ENOENT"
    ? new Error(`${command} is unavailable`)
    : new Error(`cannot start ${command}: ${errorText(error)}`);
}

export async function runLSPQuery(options: LSPQueryOptions): Promise<LSPQueryResult> {
  const child = spawn(options.command, options.args, {
    cwd: options.workspace,
    stdio: ["pipe", "pipe", "pipe"],
  });
  const started = new Promise<Error | null>((resolve) => {
    child.once("spawn", () => resolve(null));
    child.once("error", (error) => resolve(error));
  });
  const completion = new Promise<Error | null>((resolve) => {
    child.once("error", (error) => resolve(error));
    child.once("close", () => resolve(null));
  });
  const deadline = new Promise<never>((_, reject) => {
    setTimeout(() => reject(new Error("deadline exceeded")), 30_000);
  });
  const stderrPromise = collect(child.stderr);
  const startError = await started;
  if (startError !== null) {
    await stderrPromise;
    throw processFailure(options.command, startError);
  }
  const connection = createMessageConnection(child.stdout, child.stdin, {
    error() {},
    warn() {},
    info() {},
    log() {},
  });
  const workspaceURI = pathToFileURL(options.workspace).href;
  const workspaceName = options.workspace.split(/[\\/]/u).at(-1) ?? options.workspace;
  connection.onRequest("workspace/configuration", (params: unknown) => {
    const items = params !== null && typeof params === "object" && !Array.isArray(params)
      ? (params as {items?: unknown}).items
      : null;
    return Array.isArray(items) ? items.map(() => null) : [];
  });
  connection.onRequest("workspace/workspaceFolders", () => [
    {uri: workspaceURI, name: workspaceName},
  ]);
  connection.onRequest("client/registerCapability", () => null);
  connection.onRequest("window/workDoneProgress/create", () => null);
  connection.listen();

  let phase = "initialize";
  try {
    const initialized = await Promise.race([
      connection.sendRequest("initialize", {
        processId: process.pid,
        clientInfo: {name: "hpatch", version: "1"},
        rootUri: workspaceURI,
        workspaceFolders: [{uri: workspaceURI, name: workspaceName}],
        capabilities: {
          general: {positionEncodings: ["utf-16"]},
          workspace: {configuration: true, workspaceFolders: true},
          textDocument: {
            definition: {linkSupport: true},
            references: {},
            synchronization: {},
          },
        },
      }),
      deadline,
    ]);
    const positionEncoding = initialized !== null && typeof initialized === "object"
      ? (initialized as {capabilities?: {positionEncoding?: unknown}}).capabilities?.positionEncoding
      : undefined;
    if (positionEncoding !== undefined && positionEncoding !== "utf-16") {
      throw new Error(`language server selected unsupported position encoding ${String(positionEncoding)}`);
    }
    phase = "open document";
    await connection.sendNotification("initialized", {});
    const uri = pathToFileURL(options.path).href;
    await connection.sendNotification("textDocument/didOpen", {
      textDocument: {
        uri,
        languageId: options.languageID,
        version: 1,
        text: options.source,
      },
    });
    phase = options.mode === "def" ? "definition" : "references";
    const response = options.mode === "def"
      ? await Promise.race([
        connection.sendRequest("textDocument/definition", {
          textDocument: {uri},
          position: options.position,
        }),
        deadline,
      ])
      : await Promise.race([
        connection.sendRequest("textDocument/references", {
          textDocument: {uri},
          position: options.position,
          context: {includeDeclaration: true},
        }),
        deadline,
      ]);
    const parsedLocations = locations(response, options.mode);
    phase = "shutdown";
    // Cleanup is auxiliary once the semantic response is complete. Bound the
    // child lifetime even when a server ignores shutdown or exit.
    const cleanupTimeout = setTimeout(() => child.kill("SIGKILL"), 1_000);
    let cleanupError: Error | null = null;
    try {
      const shutdownCompleted = await Promise.race([
        connection.sendRequest("shutdown").then(() => true, () => false),
        completion.then(() => false),
      ]);
      if (shutdownCompleted) {
        await connection.sendNotification("exit");
        child.stdin.end();
      } else {
        child.kill("SIGKILL");
      }
    } catch {
      // A completed semantic response remains valid when the server closes its
      // connection during shutdown. Reap the invocation rather than replacing it.
      child.kill("SIGKILL");
    }
    const completionError = await completion;
    clearTimeout(cleanupTimeout);
    if (completionError !== null) {
      cleanupError = completionError;
    }
    const stderr = semanticStderr(decodeUTF8(await stderrPromise, "language server stderr"));
    return {
      locations: parsedLocations,
      stderr: cleanupError !== null && stderr === "" ? semanticStderr(`child error: ${errorText(cleanupError)}`) : stderr,
    };
  } catch (error) {
    child.kill("SIGKILL");
    const completionError = await completion;
    await stderrPromise;
    if (completionError !== null && "code" in completionError && completionError.code === "ENOENT") {
      throw processFailure(options.command, completionError);
    }
    const message = error instanceof Error ? error.message : errorText(error);
    throw new Error(`${phase} failed: ${message}`);
  } finally {
    connection.dispose();
  }
}
