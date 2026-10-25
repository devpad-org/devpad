// How credentials found in a workspace are named to the user.

const CREDENTIAL_KINDS: Record<string, string> = {
  'ssh-key': 'SSH private key',
  'gh-token': 'GitHub CLI token',
  'git-credentials': 'Git credential store',
  netrc: 'netrc credentials',
  'credential-helper': 'Git credential helper',
  'url-rewrite': 'URL rewrite with a token',
  'http-header': 'Git HTTP header',
  'remote-url': 'Remote URL with a token',
  'config-include': "Git config include devpad couldn't read",
}

export function credentialKind(kind: string): string {
  return CREDENTIAL_KINDS[kind] ?? kind
}

// Shown after every conversion in place: devpad's search can't be complete.
export const REVOKE_REMINDER =
  'devpad only checks known places; revoke any other token or key you used in this workspace.'
