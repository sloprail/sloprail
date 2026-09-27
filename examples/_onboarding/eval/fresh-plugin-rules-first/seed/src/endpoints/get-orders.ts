import { Router } from "express";
import { prisma } from "../db";

const router = Router();

router.get("/orders", async (req, res) => {
  const userId = typeof req.query.userId === "string" ? req.query.userId : undefined;
  const orders = await prisma.order.findMany({
    where: userId ? { userId } : undefined,
    orderBy: { createdAt: "desc" },
  });
  res.json({ orders });
});

export default router;
