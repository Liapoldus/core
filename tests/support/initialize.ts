import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

export async function initializeCore(_binary: string, directory: string, managementAddress: string, controlAddress: string) {
  const database = join(directory, "core.sqlite");
  const certificate = join(directory, "management.crt");
  const privateKey = join(directory, "management.key");
  const controlCertificate = join(directory, "control.crt");
  const controlKey = join(directory, "control.key");
  const replicaClientCA = join(directory, "replica-client-ca.crt");
  const replicaServerCA = join(directory, "replica-server-ca.crt");
  await Promise.all([
    execFileAsync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1", "-keyout", privateKey, "-out", certificate]),
    execFileAsync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=plugin-control", "-keyout", controlKey, "-out", controlCertificate]),
    execFileAsync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=plugin-replica-ca", "-keyout", join(directory, "replica-client-ca.key"), "-out", replicaClientCA]),
    execFileAsync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=plugin-replica-server-ca", "-keyout", join(directory, "replica-server-ca.key"), "-out", replicaServerCA]),
  ]);
  const environment: NodeJS.ProcessEnv = {
    ...process.env,
    CORE_SQLITE_PATH: database,
    CORE_INIT_MANAGEMENT_LISTEN: managementAddress,
    CORE_INIT_MANAGEMENT_CERTIFICATE: certificate,
    CORE_INIT_MANAGEMENT_KEY: privateKey,
    CORE_INIT_CONTROL_LISTEN: controlAddress,
    CORE_INIT_CONTROL_PUBLIC_URL: `https://${controlAddress}`,
    CORE_INIT_CONTROL_CERTIFICATE: controlCertificate,
    CORE_INIT_CONTROL_KEY: controlKey,
    CORE_INIT_REPLICA_CLIENT_CA: replicaClientCA,
    CORE_INIT_REPLICA_SERVER_CA: replicaServerCA,
    CORE_INIT_SECRET_ROOT: directory,
  };
  const bootstrap = await execFileAsync("go", ["run", "./tests/fixtures/runtime-bootstrap"], { cwd: coreRoot, env: environment });
  return { database, environment, bootstrapToken: bootstrap.stdout.trim() };
}
