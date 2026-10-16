# Reference

The Artifik API as it was observed. The client in `internal/artifik` is written from this directory and never from a guess.

Artifik (`https://app.artifik.no`) is a procurement platform: buyers publish competitions, suppliers bid. The web client is a React Router (Remix) app behind CloudFront. There is no separate API host and no public documentation. No `llms.txt`, no `robots.txt`.

## How data comes out

Every page has a loader. `GET <path>.data` answers with the loaders' data as a turbo-stream (`content-type: text/x-script`). `?_routes=<route id>` asks for one route only, which leaves out `root` and `translations` (30 kB of translations per request).

The root path is `/_root.data`. Route ids are in the build manifest, `assets/manifest-<hash>.js`.

### Turbo-stream, as far as the client needs it

The first line is a JSON array. Element 0 is the root value. Inside an object, `{"_<k>": <v>}` means the key is the string at index `k` and the value is the element at index `v`. Inside an array, every element is an index. A negative index is a constant: -5 null, -7 undefined, -1 a hole, -2 NaN, -3 and -6 the infinities, -4 negative zero. A top level element that is an array beginning with a one letter string is a typed value: `["D", ...]` a date, `["P", n]` a promise (later lines `P<n>:`), and so on. Dates in the observed answers were ISO strings already. A redirect is a 202 with `x-remix-response: yes` and the body `[["SingleFetchRedirect",1],{"redirect":...,"status":302}]`.

The decoded root is `{"<route id>": {"data": ...}}`, or `{"error": ...}` for a route that failed.

## Verified

2026-10-16, against the real service.

**Public, without a login**

- `GET /procurements.data?_routes=routes/procurements` lists published procurements. Answer: `procurements` (100 per page), `total`, `pagination {lastPage, nextPage, prevPage}`, `filterableFields.organizations` (a map keyed by organization slug).
  - Query: `search` (free text), `procurementTypes` (one type; a comma list was taken, a repeated parameter kept only the first), `orgIds`, `page` (from 1). A page past the last answers an empty list, not an error.
  - Types the web client offers: `RFI`, `INFORMATION`, `INFORMATION_INDICATIVE`, `COMPETITION`, `DPS`, `QUALIFICATION_ANNOUNCEMENT`, `NOTICE_OF_INTENT`, `PLANNING`.
  - Order: open procurements mostly first, then older ones. It is not sorted by deadline or by id, and two requests a minute apart gave different first ids. All 14 pages in one sweep gave 1362 distinct ids.
  - Item: `id` (number), `name`, `type`, `phase` (text such as "Preparing bids", "Cancelled", "Completed"), `description` (HTML), `currentDeadline {id, date, type}` (types seen: submission, qualification, announcement, award decision, contract signing, acceptance, planned), `organization {id, name, ...}`, `budget` (number), `place`, `lots`, `awardCriteria`, `isPublished`, `hasStarted`, `isPrequalified`, `cancelingReason`.
  - "Can still bid": `currentDeadline.type` submission or qualification, date in the future, phase not Cancelled. About 200 of 1362.
- `GET /procurements/<id>.data?_routes=routes/procurements_.$proc_id` is one procurement: `procurement` with about 90 fields. Used: `id`, `name`, `type`, `procedure` ("Open"), `regulation`, `threshold`, `phase`, `status`, `description` (HTML, may be short), `organization`, `currentDeadline`, `timeline [{name, date, type}]` (names such as ANNOUNCEMENT, DEADLINE_TO_ASK_QUESTIONS, SUBMISSION, AWARD_DECISION, CONTRACT_SIGNING, ACCEPTANCE), `lots [{id, name, description, duration_months, estimated_value, ...}]`, `cpv_codes {main_cpv, aditional_cpv}` (sic), `total_value`, `currency`, `performance_places`, `contractCategory`, `externalId`, `externalReferenceUrl`, `files`.
  - `lots[]` also has `selectionCriteria [{title, eFormsType, is_documentation_required, description, smartDocRequest}]` (the qualification requirements, eForms codes such as `slc-sec-inf`), `awardCriteria [{title, awardCriteriaType (price, quality), percentage, description}]`, and `requirementsSmartDoc` (the "Spesifikasjon" section). A lot can have no name: then it is the whole procurement. Its description can be the procurement's own, word for word.
  - `selectionCriteria[].description` is the requirement, and `smartDocRequest` is the documentation the supplier hands in to prove it (two different texts, both shown).
  - `procurementInformationSmartDoc` is the competition document ("Konkurransedetaljer", what attachments call "KGV"): chapters with headings h1 to h4, about 23 000 characters on 9684, and 4 bytes (empty) on 10022, which has its competition document as an attachment.
  - Also used: `estimatedContractStartDate`, `reduce_participants_by_selection_criteria` ("__YES__"/"__NO__"), `suppliersLimitReason` (free text). Yes/no fields are written "__YES__", "__NO__" or "__UNANSWERED__".
  - A smart doc is `{id, version, docJSON}`, where `docJSON` is a ProseMirror document. Node types seen (2026-10-16, procurements 9684 and 10022): doc, heading, paragraph, text, hardBreak, bulletList, listItem, bold, italic, link, textStyle, and Artifik's own `request` (`attrs.type` "files", `attrs.config.required`), `task` and `files`. A `request` is something the supplier is asked to give.
  - `files [{name, lastModified, orderIndex, isDeleted, key}]`. `key` is `procurementAttachments/<id>/...`.
  - An unknown or malformed id answers **HTTP 500**, not 404.
- `GET /procurements/<id>/qas.data?_routes=routes/procurements_.$proc_id.qas` is the public questions and answers: `qas [{id, title, question, answer, publishedDate, files}]`.
- `POST /api/manage-files`, form encoded: `endpoint=publicProcurement`, `intent=download`, `file_name=<name>`, `proc_id=<id>`. Answer `{"url": "<presigned S3 URL>"}`, valid for 300 seconds, on `artifikinfrastructurestack-documents...s3.eu-central-1.amazonaws.com`. The URL is fetched without any login. Works without a login. It reads only: the web client's tracking call (`/api/jobs?intent=markDownloadStarted`) belongs to the zip download, not to this.

**With a login**

- Login is SMS code, Google, or other single sign-on, with reCAPTCHA v3. The tool takes over a browser login.
- The login is one cookie, `artifik-session` on `app.artifik.no`, HttpOnly, `Max-Age=604800` (7 days). Its value is `<base64 JSON>.<signature>`, signed by the server. The JSON holds `accessToken` (a Cognito access JWT, `eu-central-1`, lifetime 7200 s) and `refreshToken` (a Cognito refresh token, not a JWT).
- `GET /api/ensure-session` with the cookie renews: it answers `{"success":true}` and a new `artifik-session` cookie with a new access token (new `iat`) and the same refresh token. It renewed while the old access token was still good. Without a cookie, or with a bad one, it answers `401 {"error":"No refresh token"}`.
- `GET /_root.data?_routes=routes/_index` is the user's own procurements ("My bids"): `allProcurements`, `total`, `pagination {currentPage, totalPages, pageSize}`. Query: `page`, `pageSize` (both honoured), `status` (PUBLISHED), `supplierStatus` (DRAFT_IN_PROGRESS). Item adds `status`, `listType` ("bidder"), `tenderId`, `supplierInfo {supplierStatus, supplierOrgName}` (supplierStatus seen: SUBMITTED_BID, CANCELLED).
- Logged out, the same request answers a 202 single fetch redirect to `/login?redirectTo=/`.

## Reported by others

Nothing. No open client was found.

## Unverified

- How long the Cognito refresh token lasts. Cognito's default is 30 days. The cookie says 7 days, and each renewal sets it again.
- What the server does with a cookie whose access token has run out on a request other than `/api/ensure-session`. The tool renews through `ensure-session` before the access token runs out, so it should not matter.
- The zip download (`/api/jobs?intent=createZipJob&proc_id=<id>&endpoint=publicProcurement&jobType=filesZipJob`, then a websocket and `intent=getJobResult`). Not used: the tool fetches the files one by one, which is what the zip job does too.
- Private procurements a supplier is invited to (`endpoint=privateProcurement`). Not used.
