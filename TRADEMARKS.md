# Name and logo policy

Marineris is free software under the [AGPL-3.0](LICENSE). Its branding — the
files in [`branding/`](branding/) — is under the same licence with
**additional terms** allowed by its section 7, written in
[`branding/LICENSE`](branding/LICENSE). In short:

- **Section 7(c), origin of the material:** a modified version must not be
  presented as Marineris or as an official edition of it, and modified
  versions of the branding must be marked as different from the original.
- **Section 7(e), trademarks:** the licence grants no right to use the name
  "Marineris" or its logos as the name or logo of another product or service.

## What this means for forks

You are welcome to fork Marineris. If you **distribute** a modified version
(published source, images, packages) or **offer it to others over a network**,
give it its own identity:

- use a different name;
- replace the logo, wordmark and icons in `branding/` with your own.

Saying that your project is *based on* or a *fork of* Marineris, with a link
to this repository, is fine and appreciated.

You do not need to rebrand to:

- run Marineris, modified or not, on your own servers;
- send changes back to this project;
- distribute unmodified releases under the Marineris name;
- write about, review, or package unmodified Marineris.

## How to rebrand

Replace the files in `branding/` with your own (same names) before building;
everything the UI shows comes from there, and `theme.json` sets the colours.
Without rebuilding, an installation can also override them with
`MARINERIS_BRANDING_DIR` (see [`branding/README.md`](branding/README.md)).
