---
title: file-guard
description: A rule bound to a file's state, re-checked until the file is right.
kind: explanation
sidebar:
  order: 1
---

A **file-guard** is a [nature](/concepts) bound to a file's **state**, not to an event — it re-checks the file every cycle until its content is right, and can run *preventively* to refuse a bad write before it lands.

Reach for it when the thing you're protecting is a property of a **file**.

→ [Authoring: the file-guard nature](/guides/file-guard)
