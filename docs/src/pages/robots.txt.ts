import type { APIRoute } from "astro";

export const prerender = true;

export const GET: APIRoute = ({ site }) => {
  const publicRelease = process.env.DOCS_PUBLIC_RELEASE === "true";
  const body = publicRelease
    ? `User-agent: *\nAllow: /\nSitemap: ${new URL("/sitemap-index.xml", site).href}\n`
    : "User-agent: *\nDisallow: /\n";

  return new Response(body, {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
  });
};
