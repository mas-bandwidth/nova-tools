## The loop, in words

```
pool ──► cut ──► launch ══(nova-swarm batch, --then harvest)══► harvest ──► pool ──► …
 │                 │                                              │
 │  candidates     │  every free slot is filled with admitted work;│  done: push, PR, read card
 │  from sources   │  the rest wait in queue.tsv, never dropped    │  abstain: retry.tsv, no push
 │                 │                                              │  then pool, cut, launch again
 └─ empty? say so: PULSE POOL EMPTY. Never pad. ◄─────────────────┘
```

**Scatter** is `launch`: every card that can run now runs now, one slot each, one batch id,
one deadline. **Gather** is `harvest`: it reads the swarm's packet, never a report body, and
disposes each card by its own two lines. **Scatter again** is the last thing `harvest` does.
The loop ends only when the pool and the queue are both empty, and then it says so.
