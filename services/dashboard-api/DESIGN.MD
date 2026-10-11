# Kit Room: the dashboard page

The page that dashboard-api serves at `/` (and the gateway forwards to at `/`). It is the
whole front end of the platform: ordering equipment for new starters, then running
fulfilment, stock, payments, shipping, notifications and service health.

## The idea: a kit of parts

In engineering, a *kit of parts* is a set of standard components that combine into a whole.
Equipping a new starter is exactly that, so the page treats every order as a drawing set:

- the catalogue is a **parts sheet**: each product drawn as a line drawing, numbered P-01, P-02…
- the order is a **bill of materials**: numbered lines, quantities, unit and line amounts
- fulfilment is a **pipeline**: Placed → Confirmed → Processing → Shipped → Delivered, the
  same stages the worker and order-service move an order through
- each view is a numbered **sheet** (Sheet 01 Kit … Sheet 08 Systems)

**The signature moment is the plan.** A blueprint of one workstation, in CoderCo navy. Add a
part and a pen head travels to its place on the desk and plots it in coral. The drawing
then cools to white and gets a callout with its bill-of-materials line number. Quantities
over one get a dashed echo behind the part. When the order is placed, the title block
shows the order number and live status, and an ISSUED stamp lands on the drawing. The
plan only animates when it is on screen. Parts added while it is off screen are drawn when
it scrolls back into view, and with reduced motion they appear at once.

Why it fits CoderCo: they build engineers, and their brand is navy, coral and a monospace
wordmark. Technical drawing, monospace data and a CI/CD-style pipeline are the language of
the people this tool is for. The coral is used as the pen, the one thing that is changing
or needs action, not as decoration.

## System

| | |
|---|---|
| Colour | Navy `#242E46` (ink, plan ground), coral `#FF7759` (pen: fills or on navy only), paper `#F4F2EE`, sheet `#FBFAF7`. Coral text on paper uses `#B8401F` (4.9:1). Text never goes lighter than `#5B6480` (5.2:1). Order stages use one navy ramp, light to dark, so they read as progress; cancelled is red. |
| Type | Archivo, expanded (`font-stretch: 125%`, ~760) for display; Archivo at normal width for text; JetBrains Mono for data, labels, SKUs and money, echoing the CoderCo wordmark. Both embedded, see `fonts/`. |
| Grid | 12 columns, max width 1440, side gutter `clamp(16px, 4vw, 56px)`. The kit splits 6/6 with the board (plan + bill) sticky on the right. |
| Surfaces | Hairline borders, square corners (3px controls, 6px sheets). Shadows only for things that float: toasts, the mobile sheet, the kit bar. No gradients, no glass. |
| Drawings | Orthographic line work in millimetres, one stroke weight, no fills (`DRAWINGS` in the script). The kind of part is read from its name or id (`KINDS`); unknown parts are drawn as a labelled parcel. To add a kind, add a drawing, a `KINDS` rule and a desk `SLOTS` entry. |
| Motion | Ease `cubic-bezier(.2,.8,.2,1)`; 120ms hover, 220ms state, 420ms reveals, ~0.8–1.2s for a plotted part. Only the plan and the order stamp get choreography; everything else is a short fade or slide. `prefers-reduced-motion` turns it all off. |
| Charts | Single-series columns ≤ 24px with 4px rounded caps, hairline grid, today in coral. Every chart has hover, arrow-key reading and a table view. The stage ramp passed the dataviz palette validator. |

## Rules the page keeps

- **Only real data.** Every figure comes from an endpoint the gateway already exposes. The
  part kind is the only thing inferred, and only to choose a drawing. Overview figures are
  platform-wide; the order book shows the signed-in account's orders, as order-service
  filters them. Labels say which.
- **The workflow is unchanged.** Same auto-login, same Idempotency-Key retry rule, no
  manual pending → confirmed. Orders now carry several lines and an optional note, which
  `POST /api/orders/` already accepted.
- **One file.** CSS, script, drawings and fonts are inline because the gateway forwards
  only `/` to this page without a token; a separate asset would need a gateway change.
- **Safe by default.** All markup goes through the `html` tag, which escapes every value
  from the API. There are no inline event handlers; one delegated listener per event type.
- **Accessible.** Real links and buttons, skip link, visible focus (`#B8401F` on paper, coral
  on navy), focus kept when controls are re-rendered, labelled steppers, a modal review sheet
  on small screens with focus trap and Escape, table views for charts, 44px touch targets.

## Working on it

There is no build step. Edit `static/index.html`, then
`docker compose up -d --build dashboard-api`. The browser-side state kept is the draft kit
(`localStorage` key `kitroom.kit.v1`), for convenience only.