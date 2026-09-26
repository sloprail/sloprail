import express from "express";
import getUsers from "./endpoints/get-users";
import getOrders from "./endpoints/get-orders";

export const app = express();
app.use(express.json());

app.use(getUsers);
app.use(getOrders);
