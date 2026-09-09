import { isIP } from "node:net";

const AUTHORITY_PATTERN = /^[a-z][a-z\d+.-]*:\/\/([^/?#]*)/i;
const HOSTNAME_PATTERN =
  /^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i;

function authority(value) {
  return value.match(AUTHORITY_PATTERN)?.[1];
}

function hasCredentials(value) {
  return authority(value)?.includes("@");
}

function hasOnlyOriginPath(value) {
  const match = value.match(AUTHORITY_PATTERN);
  if (!match) return false;

  const remainder = value.slice(match[0].length);
  return remainder === "" || remainder === "/";
}

function hasExplicitPort(value) {
  const valueAuthority = authority(value);
  if (!valueAuthority) return false;

  const hostPort = valueAuthority.slice(valueAuthority.lastIndexOf("@") + 1);
  if (hostPort.startsWith("[")) return hostPort.includes("]:");
  return hostPort.includes(":");
}

function invalidOrigin(message) {
  throw new Error(`PUBLIC_SITE_ORIGIN ${message}`);
}

export function validatePublicOrigin(value) {
  if (typeof value !== "string" || value.length === 0) {
    invalidOrigin("is required in public mode");
  }
  if (
    value !== value.trim() ||
    /[\u0000-\u001f\u007f]/.test(value) ||
    value.includes("\\") ||
    value.includes("?") ||
    value.includes("#")
  ) {
    invalidOrigin("must be a valid HTTPS origin");
  }

  let url;
  try {
    url = new URL(value);
  } catch {
    invalidOrigin("must be a valid HTTPS origin");
  }

  if (url.protocol !== "https:") {
    invalidOrigin("must use HTTPS");
  }
  if (hasCredentials(value) || url.username || url.password) {
    invalidOrigin("must not contain credentials");
  }
  if (hasExplicitPort(value)) {
    invalidOrigin("must not contain a port");
  }
  if (!hasOnlyOriginPath(value) || url.pathname !== "/") {
    invalidOrigin("must contain only the HTTPS origin");
  }

  const hostname = url.hostname.toLowerCase();
  if (
    !hostname ||
    hostname.endsWith(".") ||
    hostname === "pages.dev" ||
    hostname.endsWith(".pages.dev") ||
    isIP(hostname) !== 0 ||
    !HOSTNAME_PATTERN.test(hostname)
  ) {
    invalidOrigin("must be an approved HTTPS custom domain");
  }

  return url.origin;
}
