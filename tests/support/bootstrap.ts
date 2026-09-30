export interface DeclaredReplica {
  instanceId: string;
  replicaId: string;
  endpoint: string;
  commonName: string;
  uniformResourceIdentifier?: string;
}

export interface BootstrapTLSFiles {
  certificate: string;
  key: string;
  replicaClientCA: string;
  replicaServerCA: string;
}

export interface BootstrapOptions {
  state: string;
  managementListen: string;
  managementCertificate: string;
  managementKey: string;
  managementClientCA?: string;
  controlListen: string;
  controlPublicURL: string;
  controlTLS: BootstrapTLSFiles;
  replicas: DeclaredReplica[];
  requestLimits?: string[];
}

// writeBootstrap renders a v1 core.yaml. The plugins block is the normative
// operator registry: instanceId, replicaId, fixed endpoint and expected peer
// identity. It is intentionally rendered here so no test can silently drift
// into inventing a registration API instead of declaring endpoints.
export function renderBootstrap(options: BootstrapOptions): string {
  const management = [
    "state:",
    `  path: ${options.state}`,
    "management:",
    `  listen: ${options.managementListen}`,
    "  tls:",
    `    certificate: file:${options.managementCertificate}`,
    `    key: file:${options.managementKey}`,
  ];
  if (options.managementClientCA) {
    management.push(`    clientCA: file:${options.managementClientCA}`);
  }
  for (const limit of options.requestLimits ?? []) {
    management.push(`  ${limit}`);
  }
  const control = [
    "pluginControl:",
    `  listen: ${options.controlListen}`,
    `  publicURL: ${options.controlPublicURL}`,
    "  tls:",
    `    certificate: file:${options.controlTLS.certificate}`,
    `    key: file:${options.controlTLS.key}`,
    `    replicaClientCA: file:${options.controlTLS.replicaClientCA}`,
    `    replicaServerCA: file:${options.controlTLS.replicaServerCA}`,
  ];
  const plugins = ["plugins:"];
  let current = "";
  for (const replica of options.replicas) {
    if (replica.instanceId !== current) {
      current = replica.instanceId;
      plugins.push(`  - instanceId: ${replica.instanceId}`);
      plugins.push("    replicas:");
    }
    plugins.push(`      - replicaId: ${replica.replicaId}`);
    plugins.push(`        endpoint: ${replica.endpoint}`);
    plugins.push("        expectedPeerIdentity:");
    plugins.push(`          commonName: ${replica.commonName}`);
    if (replica.uniformResourceIdentifier) {
      plugins.push(`          uniformResourceIdentifier: ${replica.uniformResourceIdentifier}`);
    }
  }
  return [...management, ...control, ...plugins, ""].join("\n");
}
