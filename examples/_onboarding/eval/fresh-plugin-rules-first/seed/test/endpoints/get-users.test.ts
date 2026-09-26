import { describe, expect, it, vi } from "vitest";
import request from "supertest";

vi.mock("../../src/db", () => ({
  prisma: { user: { findMany: vi.fn().mockResolvedValue([{ id: "u1", email: "a@b.c", name: "A" }]) } },
}));

import { app } from "../../src/app";

describe("GET /users", () => {
  it("lists users", async () => {
    const res = await request(app).get("/users");
    expect(res.status).toBe(200);
    expect(res.body.users).toHaveLength(1);
  });
});
