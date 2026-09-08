import { docsLoader, i18nLoader } from "@astrojs/starlight/loaders";
import { docsSchema, i18nSchema } from "@astrojs/starlight/schema";
import { defineCollection } from "astro:content";
import { z } from "astro/zod";

const operatorFrontmatter = z.object({
  availability: z.enum(["available", "preview", "planned"]),
  sources: z.array(z.string().min(1)).min(1),
  translationKey: z.string().min(1),
  journeyOrder: z.number().int().positive().optional(),
});

export const collections = {
  docs: defineCollection({
    loader: docsLoader(),
    schema: docsSchema({ extend: operatorFrontmatter }),
  }),
  i18n: defineCollection({ loader: i18nLoader(), schema: i18nSchema() }),
};
