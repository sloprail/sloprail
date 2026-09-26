import { Router } from "express";
import { prisma } from "../db";

const router = Router();

router.get("/users", async (_req, res) => {
  const users = await prisma.user.findMany({ orderBy: { createdAt: "desc" } });
  res.json({ users });
});

export default router;
