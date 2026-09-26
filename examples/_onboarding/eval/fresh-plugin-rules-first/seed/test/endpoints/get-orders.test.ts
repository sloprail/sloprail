import { describe, expect, it, vi } from "vitest";
import request from "supertest";

const { findMany } = vi.hoisted(() => ({
  findMany: vi.fn().mockResolvedValue([{ id: "o1", userId: "u1", totalCents: 1200, status: "paid" }]),
}));
vi.mock("../../src/db", () => ({ prisma: { order: { findMany } } }));

import { app } from "../../src/app";

describe("GET /orders", () => {
  it("lists orders", async () => {
    const res = await request(app).get("/orders");
    expect(res.status).toBe(200);
    expect(res.body.orders).toHaveLength(1);
  });

  it("filters by userId", async () => {
    await request(app).get("/orders?userId=u1");
    expect(findMany).toHaveBeenLastCalledWith(expect.objectContaining({ where: { userId: "u1" } }));
  });
});
