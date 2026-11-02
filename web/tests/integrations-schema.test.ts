import { describe, expect, it } from "vitest";
import {
  defaultsFromSchema,
  groupByCategory,
  schemaProperties,
} from "@/features/integrations/helpers/schema";

describe("integrations schema helpers", () => {
  const schema = {
    type: "object",
    required: ["webhookUrl", "password"],
    properties: {
      webhookUrl: {
        type: "string",
        format: "uri",
        title: "URL",
        default: "https://example.test",
      },
      password: { type: "string", writeOnly: true, title: "Password" },
      flag: { type: "boolean", title: "On", default: true },
    },
  };

  it("lists properties with secrets", () => {
    const props = schemaProperties(schema, ["password"]);
    expect(props).toHaveLength(3);
    const pw = props.find((p) => p.key === "password");
    expect(pw?.writeOnly).toBe(true);
    expect(pw?.required).toBe(true);
    expect(props.find((p) => p.key === "webhookUrl")?.format).toBe("uri");
  });

  it("defaults skip writeOnly", () => {
    const d = defaultsFromSchema(schema);
    expect(d.webhookUrl).toBe("https://example.test");
    expect(d.flag).toBe(true);
    expect(d.password).toBeUndefined();
  });

  it("groups by category order", () => {
    const groups = groupByCategory([
      { category: "spam", id: "1" },
      { category: "chat", id: "2" },
      { category: "custom", id: "3" },
    ]);
    expect(groups.map((g) => g.category)).toEqual(["chat", "spam", "custom"]);
  });

  it("handles empty schema", () => {
    expect(schemaProperties(null)).toEqual([]);
    expect(defaultsFromSchema(undefined)).toEqual({});
  });
});
