# Plan: Scoped API keys

An API key currently carries its owner's whole role. A credential issued so a
bot can register one URL can also delete every image, and there is no way to
issue anything narrower.

## Status

Design sketched 2026-10-15. Not started. Not blocking anything — see
[Why now](#why-now-and-why-not).

## The problem

`auth.authenticateAPIKey` looks up the key's owner and copies their role:

```go
role := config.Role(owner.Role)
return &Principal{ ... Role: role, ViaAPIKey: true }, nil
```

Authorisation then reads that role. So a key is exactly as powerful as the
account it belongs to, and an account is one of four fixed values: User,
TrustedUser, Moderator, Admin.

There is no third way to be trusted *partially*, and the API needs one. The bot
is the concrete case:

| It needs | It should not have |
| --- | --- |
| `GET /instances/admin` | list every user's Discord ID |
| `POST /instances` | delete a tag |
| `DELETE /instances` | delete every object in the bucket |

It holds an Admin key today, which means `DELETE /storage-diff` — permanent
deletion of catalogue records and stored objects — is one bug away.

Minting it under a lower role does not help. The instance routes hard-require
`RoleAdmin` via `requireRole`, deliberately, because the same admin decides them.

The failure mode is not the key leaking to a stranger. It is an ordinary code
change doing something reasonable-looking to a scope nobody wrote down.

## Options

**Leave it.** One key, one role, held by one process you control, revocable from
`/auth/api-keys`. Costs nothing to build and is a real answer when the number of
holders is one and you trust that process.

**Take the write path out of the bot.** Registration becomes something you do
from a panel; the bot only grants the role. No credential in the bot at all,
which is the strongest version of the answer and needs no new concept.

**Scope a key to a list of actions.** What the rest of this document is about.

**Mint per-purpose service accounts.** An Admin account per job, so a bug is
contained by which key leaked rather than by what the code is allowed to do.
Cheap — it is the existing mechanism used more carefully — but the blast radius
of any one key is still the whole role.

## What scoped would look like

A second credential kind alongside the existing one, not a replacement:

```
ApiKeys
  ... existing columns ...
  "Scopes"      jsonb NULL     -- NULL means "the owner's full role"
```

`NULL` is the important design decision: it means every key issued before this
lands keeps working with exactly the authority it has now. A key with no scopes
is not "no access"; it is "unrestricted", which is the current behaviour and the
only safe default for anything already issued.

An authenticated request then carries **both** a role and a scope set. The gate
becomes a conjunction, and the existing ones are unchanged:

- `requireRole(Admin)` — a scoped key still has to belong to an admin.
- `requireScope("instances:write")` — and has to name the capability.

The conjunction is what makes this worth the work. A scope on its own would let
a User-level key write instances; a role on its own is what we have now.

### Naming scopes

Capability names, not route names, and never method verbs on paths — those change
when the API does. A first cut:

```
instances:read   instances:write
images:delete    tags:delete        artists:delete
users:read       users:write        users:ban
reports:read     stats:read         storage:read
storage:delete
```

`storage:delete` is separated from `storage:read` because that is the one
capability where a mistake is not recoverable.

### Minting and display

`POST /auth/api-keys` takes `scopes`. `GET /auth/api-keys` returns the scopes, so
a key on the dashboard says what it can do rather than requiring the reader to
know what its owner's role can do.

### What it deliberately does not do

- **No scope inheritance.** A key's authority is what its scopes say. A key
  issued with `instances:write` on a Moderator account fails the role check,
  which is the answer, not a surprise.
- **No wildcard scopes.** `*` is how a scoped key becomes an unscoped key with
  extra steps, and someone will reach for it the first time a list is long.
- **No expiry semantics change.** `expiresInDays: 0` already means "does not
  expire", which is right for a service credential and orthogonal to scope.

## Open questions

1. **Does a scope also constrain row-level behaviour?** A key scoped to
   `users:read` currently sees every user, because there is no filtering layer.
   Scoping the *action* without the *rows* is a half-measure — it is still worth
   having, and it should be described honestly rather than oversold.
2. **Who may issue a scoped key?** Anyone can create a key for themselves today.
   An Admin scoping a key is a no-op; a Moderator scoping one is a promotion, so
   minting a scoped key probably has to require Admin.
3. **Does the C# oracle need to know?** No, for the same reason `InstanceIndex`
   does not: a new column on a table EF already models, or better a new table.
   EF reads what its model declares. But it does need to not break the snapshot
   contract test — see `schema_contract_test.go`.

## Why now, and why not

Now: there is exactly one key holder, it is a process you run, and it is
revocable from a screen. That is a thin enough surface to leave alone while the
feature around it is new and moving.

Not now: the instance index landed on 2026-10-15 and the endpoint set is still
changing. Scoping a credential means naming capabilities, and naming them well
needs a stable set to name. Doing it against today's surface would mean
renaming scopes later, and renaming scopes means every issued key has to be
re-issued.

So: revisit when a second holder appears — a second bot, a script, a CI job, or
a contributor — or when anything outside this repository starts writing to the
instance index. Until then the risk is a bug in code you can read, not a
credential in someone else's hands, and that is a different kind of problem from
the one scoping solves.

## Related

- `docs/docs/instances.mdx` — what an instance must expose to be listed
- `bot/README.md` — why the bot holds an admin key at all, and what that permits