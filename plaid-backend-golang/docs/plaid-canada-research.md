<!-- Snapshot of documentation research done on 2026-09-08/09 against plaid.com/docs, Plaid support articles, the public institution coverage CSV, and plaid-go v47.0.0. It is not kept in sync with Plaid; re-verify anything load-bearing against /institutions/get_by_id and the current docs before relying on it. -->

# Plaid in Canada: research notes for plaidsync

Scope: Go service (plaid-go v47.0.0), Canadian institutions CIBC (`ins_37`) and American Express (Canada) (`ins_100533`), Plaid Trial plan, `/transactions/sync` as the primary ingest path.
Sources: Plaid docs / support articles / coverage CSV as fetched 2026-09-08/09, plus the vendored Go client at `/home/xuckl/go/pkg/mod/github.com/plaid/plaid-go/v47@v47.0.0/plaid/`. Every fact carries the confidence label from the research pass, adjusted by the independent cross-check.

**Input coverage note.** The research input covered three topics: coverage/OAuth, products/Link-token fields, and sync semantics (the sync cross-check was truncated after item 17). No dedicated input existed for webhook signature verification, the general error-type taxonomy, or Sandbox webhook-firing endpoints. Those sections below contain only what the three topics incidentally established; everything else is listed under Open questions rather than filled in from memory.

---

## 1. Flags for the user

| # | Assumption / gap | Verdict | Source |
|---|---|---|---|
| 1 | Trial plan = 10 Production Items, `/item/remove` does not free slots | **Confirmed.** "You can create 10 Production Items on a Trial plan ... Removing Items created on a Trial plan (using /item/remove) will not allow you to create more Items." Every Production access_token counts. | https://plaid.com/docs/account/billing/ |
| 2 | Trial plan applies to this team | **Confirmed only if** the team was created on/after 2026-04-15 (US/Canada). Auto-approved "for most developers"; sole-proprietor / Canadian-resident eligibility not stated. | https://plaid.com/docs/account/billing/ ; https://support.plaid.com/hc/en-us/articles/16110110883479 |
| 3 | "Plaid Canada is read-only" (no money movement) | **Consistent but not stated verbatim.** Transfer, Signal, Statements, Layer, CRA and Protect are absent from the Canada product list; CSV shows `transfer=0,signal=0,statements=0` for both `ins_37` and `ins_100533`. However Auth (account/routing numbers) *is* listed for Canada and `auth=1` at CIBC (`auth=0` at Amex CA). | https://support.plaid.com/hc/en-us/articles/27895826947735 ; https://plaid.com/documents/us_institution_coverage.csv |
| 4 | Liabilities available for CIBC + Amex CA | **Confirmed in the CSV, contradicted by CIBC's marketing page, "limited coverage" per docs.** CSV (generated 2026-08-12) has `liabilities=1` for both; https://plaid.com/institutions/cibc/ lists only Assets/Auth/Balance/Transactions; docs say "Canada coverage is limited". Only 18 of 192 CA rows carry `liabilities=1`. Treat as best-effort; confirm via `/institutions/get_by_id`. | https://plaid.com/docs/liabilities/ ; https://plaid.com/docs/api/products/liabilities/ |
| 5 | CIBC uses OAuth | **Contradicted at the country level, unverifiable per institution.** "OAuth connections ... are not currently used by financial institutions in Canada." The CSV has no `oauth` column and no Canadian institution appears on Plaid's OAuth-institutions article. Read `Institution.Oauth` from `/institutions/get_by_id` at runtime. | https://plaid.com/docs/link/oauth/ ; https://support.plaid.com/hc/en-us/articles/15687521555607 |
| 6 | Amex CA supplementary cardholders / multiple cards | **Unverifiable.** Not documented anywhere official (docs, changelog, community). Must be observed on a real Trial Item via `/accounts/get`. | (no source) |
| 7 | Canada differs: DTM (Data Transparency Messaging) | Canada sessions receive DTM even though not subject to US rule 1033; a Dashboard use case must be selected before any Production Link. `additional_consented_products` depends on DTM enrollment. Canada-only opt-out needs an account manager (Trial teams may not have one). | https://plaid.com/docs/link/data-transparency-messaging-migration-guide/ |
| 8 | Canada differs: update-mode Account Select | Not automatic for CA (non-OAuth) Items; must set `update.account_selection_enabled=true`. US OAuth banks get it implicitly. | https://plaid.com/docs/api/link/ |
| 9 | Canada differs: consent-expiry webhook | US/CA Items get `PENDING_DISCONNECT` (not `PENDING_EXPIRATION`, which is UK/EU). The 12-month Amex consent-refresh rule is stated for US Amex only; Amex CA undocumented. | https://plaid.com/docs/link/oauth/ ; https://plaid.com/docs/link/update-mode/ |
| 10 | Canada differs: Identity / Identity Match | `identity=1` at CIBC, `identity=0` at Amex CA. Identity Match in Canada is Growth/Custom plan only and not in the Trial bundle. | https://plaid.com/docs/identity/ ; CSV |
| 11 | Canada differs: Statements | In the Trial bundle but `statements=0` for both institutions and not listed for Canada. Do not request it. | https://support.plaid.com/hc/en-us/articles/27895826947735 |
| 12 | Docs silent: CIBC known issue | Secondary source (Lunch Money KB, 2026-02-17) relays Plaid KI123004: CIBC/Simplii MFA invalidated shortly after Item creation, no ETA. Plaid changelog has no CIBC entry. Check Dashboard institution status before launch. Confidence low. | https://support.lunchmoney.app/guides/automatic-imports/institution-specific-issues |
| 13 | Docs silent: Trial "almost all institutions" | Exclusions are not enumerated (only Fidelity/Schwab named post-upgrade). Nothing excludes CIBC/Amex CA, but nothing names them either. | https://plaid.com/docs/sandbox/ |
| 14 | Docs silent: Sandbox cannot validate CIBC/Amex CA | "Institution-specific quirks: Not reflected." Only generic OAuth flow; history limits not enforced. Real shapes need Trial Items. | https://plaid.com/docs/sandbox/ |
| 15 | Not in input: webhook signature verification, general error taxonomy, `/sandbox/*` webhook firing | Not researched. See Open questions. | — |

---

## 2. Design-relevant facts

### 2.1 Coverage / OAuth

| Fact | Confidence | Source |
|---|---|---|
| CIBC = `ins_37`, country CA. Supports assets, auth, balance, identity, identity_match, income_verification, liabilities, transactions. Not: investments, investments_auth, statements, signal, transfer, protect_linked_bank, cra_*. Row: `ins_37,CIBC,CA,1,1,1,0,0,0,0,0,1,1,1,0,0,1,0,0,0,1,0`. | high (byte-identical on re-fetch) | https://plaid.com/documents/us_institution_coverage.csv |
| Amex Canada = `ins_100533`, country CA. Supports assets, balance, income_verification, liabilities, transactions. Not: auth, identity, identity_match, investments, statements, signal, transfer, cra_*. Row: `ins_100533,American Express (Canada),CA,1,0,1,0,0,0,0,0,0,0,1,0,0,1,0,0,0,1,0`. | high | same |
| Do not confuse with: `ins_114166` CIBC U.S. Personal (US), `ins_138346` CIBC U.S. Business (US), `ins_133038` CIBC Wood Gundy (CA, investments, `liabilities=0`), `ins_10` American Express (US, `auth=1,identity=1`). | high | same |
| CSV header: `institution_id,name,country,assets,auth,balance,cra_base_report,cra_cashflow_insights,cra_income_insights,cra_monitoring,cra_network_insights,identity,identity_match,income_verification,investments,investments_auth,liabilities,protect_linked_bank,signal,statements,transactions,transfer`. All 19 product columns map 1:1 to `plaid.Products` constants in `model_products.go` (lines 25-64), e.g. `PRODUCTS_AUTH`, `PRODUCTS_BALANCE`, `PRODUCTS_IDENTITY`, `PRODUCTS_LIABILITIES`, `PRODUCTS_TRANSACTIONS`, `PRODUCTS_STATEMENTS`. `Institution.Products` is `[]Products` (`model_institution.go` line 24). | high | Go client |
| CSV is a snapshot ("# Generated on: 2026-08-12T23:45:49.325Z"; HTTP Last-Modified 2026-09-09 — stamp/CDN mismatch). "This table is not updated in real time; for the most up to date data, use /institutions/get or the Institutions page in the Dashboard." | high | https://plaid.com/docs/institutions/ |
| Canada product list (support article, Aug 11 2026): Auth, Balance, Identity, Investments Move, Identity Match (Early Availability), Transactions, Investments, Enrich, Liabilities (limited coverage), Identity Verification, Monitor, Assets, Income. Statements only under US. | high | https://support.plaid.com/hc/en-us/articles/27895826947735 |
| Canada: >99% of deposit accounts covered, all Big 5 banks (CIBC is one); 192 CA rows in CSV. | high | https://support.plaid.com/hc/en-us/articles/17549112773783 |
| Canadian FIs do not currently use OAuth. OAuth support is required only for US/EU/UK integrations. | high | https://plaid.com/docs/link/oauth/ |
| `/institutions/get`, `/search`, `/get_by_id` return boolean `oauth`; true if any Items require OAuth, including mid-migration. Filter via `options.oauth`. Go: `Institution.Oauth bool` (`model_institution.go` line 38); `InstitutionsGetRequestOptions.Oauth NullableBool` (`model_institutions_get_request_options.go` line 24). | high | https://plaid.com/docs/api/institutions/ |
| `institution_id` is not stable across OAuth migrations / countries / portals; same brand may have several IDs. Store the ID from Link `onSuccess` metadata or `/item/get` per Item. | high | https://plaid.com/docs/api/institutions/ |
| If CIBC migrates to OAuth: existing Items moved to `ITEM_LOGIN_REQUIRED` over the migration window (default ~90 days); possibly a new `institution_id`. Monitor Dashboard "migrations" pane. | medium (from OAuth guide, no CA date) | https://plaid.com/docs/link/oauth/ |
| `country_codes` required on institutions endpoints; `CA` valid. Go: `COUNTRYCODE_CA CountryCode = "CA"` (`model_country_code.go` line 31); `InstitutionsGetByIdRequest.CountryCodes []CountryCode` non-omitempty (`model_institutions_get_by_id_request.go` line 26). | high | https://plaid.com/docs/api/institutions/ |
| US/CA customers: all countries in Sandbox; US + Canada in Production by default. No ticket needed for CA. (The `/link/token/create` wording "only the countries you have requested access for are shown" is the residual ambiguity — confirm on Dashboard.) | high (institutions ref) / medium (Trial form unknown) | https://plaid.com/docs/api/institutions/ |
| Trial: free Production, teams created ≥ 2026-04-15, 10 Items hard cap, `/item/remove` frees nothing. Trial products: Assets, Auth, Balance, Identity, Investments (+Refresh), Liabilities, Transactions (+Refresh), Statements. Recurring Transactions not mentioned. | high | https://plaid.com/docs/account/billing/ |
| Post-upgrade: subscription products (Transactions, Liabilities) added on Trial start billing on upgrade; monthly per Item, calendar-month UTC, not pro-rated, charged even if the Item is in error; ended only via `/item/remove`. "You will only retain Production access to any products you explicitly requested in your Production request form." | high | https://plaid.com/docs/account/billing/ |
| Trial ≠ Limited Production: "access to almost all institutions prior to full Production approval." Changelog 2026-05-20: "Trial plan customers can now get immediate access to all OAuth institutions." | high (raised from medium by cross-check) | https://plaid.com/docs/sandbox/ ; https://plaid.com/docs/changelog/ |
| Trial counts as Production for OAuth; OAuth registration (app display info, company info, MSA, security questionnaire — MSA/questionnaire are US/CA-only items) deferred until paid-plan upgrade. Chase/PNC questionnaire gate and Schwab 6-week wait are US-only. | high | https://plaid.com/docs/link/oauth/ |
| TRIAL_CONNECTION_LIMIT: HTTP 429, `error_type` RATE_LIMIT_EXCEEDED, `error_code` `TRIAL_CONNECTION_LIMIT`, message "Trial connection limit exceeded. Upgrade to Production access to continue adding bank connections." Not retryable. Numeric cap not stated on that page (it is 10 per billing page). | high | https://plaid.com/docs/errors/rate-limit-exceeded/#trial_connection_limit |
| Liabilities: US + Canada (Canada limited). Supported: type `credit` subtype `credit card`/`paypal`; type `loan` subtype `student`/`mortgage`. No transaction history in Liabilities (use Transactions). Refreshed ~once/day. | high | https://plaid.com/docs/liabilities/ ; https://plaid.com/docs/api/products/liabilities/ |
| Identity available in Canada; Identity Match in Canada only via Growth/Custom plan. `/identity/get` viable for CIBC only. | high | https://plaid.com/docs/identity/ |
| Auth and Signal require a debitable checking/savings/cash-management account (this sentence is on the accounts reference, not initializing-products — corrected attribution). Credit-type accounts cannot use Auth. | high | https://plaid.com/docs/api/accounts/ ; https://plaid.com/docs/auth/ |
| DTM: auto-enrolled for all new US/CA customers since 2024-10-31; Dashboard use case required for Production Link. Canada sessions receive DTM. Sandbox: use case not required. | high | https://plaid.com/docs/link/data-transparency-messaging-migration-guide/ ; https://plaid.com/docs/sandbox/ |
| CIBC KI123004 (MFA invalidated shortly after Item creation; affects background updates; no ETA) — secondary source, ~7 months old, unresolved status. | low | https://support.lunchmoney.app/guides/automatic-imports/institution-specific-issues |

### 2.2 Link token fields (`/link/token/create`)

Go struct: `LinkTokenCreateRequest` in `model_link_token_create_request.go`. Constructor `NewLinkTokenCreateRequest(clientName string, language string, countryCodes []CountryCode)` — all three mandatory.

| Field (Go name / json) | Fact | Confidence | Source |
|---|---|---|---|
| `Language string` (`language`, line 26) | Required. `en` and `fr` supported. Must match Link customization language or customization is not applied. | high | https://plaid.com/docs/api/link/ |
| `CountryCodes []CountryCode` (`country_codes`, line 28, non-omitempty) | Required, min 1. Controls which institutions are shown. Multiple codes ⇒ only products enabled in ALL countries are used. Customization country set must match exactly (US+CA customization not applied for `['US']`). | high | https://plaid.com/docs/api/link/ ; https://plaid.com/docs/link/customization/ |
| `Products []Products` (`products`, line 33, omitempty) | Restricts Link to institutions supporting ALL listed products ("Connectivity not supported" otherwise). Each is billed at Item creation in Production and cannot be removed from the Item. Each product needs ≥1 compatible account. | high | https://plaid.com/docs/api/link/ |
| `RequiredIfSupportedProducts []Products` (line 35) | Extracted + billed only where institution/account support it; unsupported ⇒ ignored, Item still created; an extraction error fails Item creation (per initializing-products page). No overlap with other arrays; `products` must be non-empty. Allowed: auth, identity, investments, liabilities, transactions, signal, statements, protect_linked_bank, protect_transactions. | high | https://plaid.com/docs/api/link/ ; https://plaid.com/docs/link/initializing-products/ |
| `OptionalProducts []Products` (line 37) | Failures never affect Item creation, but subscription products (Liabilities) are still billed at Item creation. Only Auth, Identity, Signal Transaction Scores, Plaid Check are bill-on-use. No billing advantage over `required_if_supported_products` for Liabilities. | high | https://plaid.com/docs/link/initializing-products/ |
| `AdditionalConsentedProducts []Products` (line 39) | Consent only: no data fetched, never fails Item creation, institutions lacking the product still shown, billed only when the endpoint is first called. Allowed: auth, balance_plus, identity, investments, investments_auth, liabilities, transactions, signal (`balance` not valid). No overlap with `products`/`required_if_supported_products`. Requires DTM enrollment. | high | https://plaid.com/docs/api/link/ |
| Personal-finance recipe | "initialize Link with Transactions, with Liabilities and Investments in the Additional Consented Products array. Then call Liabilities or Investments endpoints after the Item has been linked." Non-applicable calls fail without billing. | high | https://plaid.com/docs/link/initializing-products/ |
| DTM post-Link product adds | On DTM Items a product can be added by calling its endpoint only if it was in `additional_consented_products` or its scopes are already consented; otherwise `ADDITIONAL_CONSENT_REQUIRED` and update mode is required. Liabilities scopes: "Account and balance info, Contact info, Credit and Loans"; Transactions scopes: "Account and balance info, Contact info, Transactions" — Transactions consent does not imply Liabilities consent. `/item/get` exposes `consented_data_scopes` (value `credit_loan_info` for Liabilities). New Trial team has no 6-month history for Plaid's auto-consent assessment ⇒ request explicitly. | high | https://plaid.com/docs/link/initializing-products/ ; https://plaid.com/docs/link/data-transparency-messaging-migration-guide/ |
| `Transactions *LinkTokenTransactions` (line 77) → `DaysRequested *int32` (`model_link_token_transactions.go` line 20) | Default 90, min 1, max 730; Production floors <30 to 30. Frozen once Transactions is on the Item; `TransactionsSyncRequestOptions.DaysRequested` is ignored afterwards. More history later ⇒ `/item/remove` + relink (burns a Trial slot). Recurring Transactions wants ≥180. | high | https://plaid.com/docs/api/link/ ; https://plaid.com/docs/api/products/transactions/ |
| `Webhook *string` (line 41) | Destination for webhooks. Ignored in update mode; use `/item/webhook/update` to change an existing Item's receiver. | high | https://plaid.com/docs/api/link/ |
| `RedirectUri *string` (line 50) | No query params, no `#` (hash routing), no wildcard in the value (subdomain wildcards allowed only in the Dashboard allowlist), https in Production, http://localhost allowed in Sandbox only, custom URI schemes never. Must be in Dashboard "Allowed redirect URIs". Android: `AndroidPackageName *string` (line 52) instead, `redirect_uri` blank. | high | https://plaid.com/docs/link/oauth/ ; https://plaid.com/docs/api/link/ |
| Redirect page behaviour | Web OAuth works without `redirect_uri` (popup/new tab) but webview users break. Redirect page must re-init Link with the SAME `link_token` and `receivedRedirectUri = window.location.href` (carries `oauth_state_id`); no extra query params/fragments. | high | https://plaid.com/docs/link/oauth/ |
| `AccessToken NullableString` (line 43) | Set for update mode. Update-mode tokens expire in 30 min (new-Item tokens 4 h). `access_token` does not change after update mode — no re-exchange. | high | https://plaid.com/docs/api/link/ ; https://plaid.com/docs/link/update-mode/ |
| `Update *LinkTokenCreateRequestUpdate` (line 69) → `AccountSelectionEnabled *bool` (line 20), `ReauthorizationEnabled *bool` (22), `User *bool` (24), `ItemIds []string` (26) in `model_link_token_create_request_update.go` | `account_selection_enabled` default false; required for Account Select at US/CA non-OAuth institutions (CIBC/Amex CA). `account_filters` allowed in update mode only when true. `reauthorization_enabled`: Go comment line 21 "this field is not currently used" and it is absent from the live API reference — do not set. | high | https://plaid.com/docs/api/link/ ; https://plaid.com/docs/link/update-mode/ ; Go client |
| Update-mode rules | Omit `products` (unless adding Assets/Statements/Income/Consumer Report or Product Validations) and product-specific params; add consent via `additional_consented_products`. Still requires `client_name`, `language`, `country_codes`, `user`/`user_id`; include `redirect_uri` if normally used. Update mode for new-account selection is unavailable only in UK/EU. De-selected accounts stop returning data; new accounts get data fetched but recurring streams wait for next periodic update or `/transactions/refresh`. Items in error >24 h get an immediate transactions check after update mode. Successful update mode resets `consent_expiration_time` as if newly created. | high | https://plaid.com/docs/link/update-mode/ ; https://plaid.com/docs/api/link/ |
| `LinkCustomizationName *string` (line 47) | Customization applied only on exact country + language match. | high | https://plaid.com/docs/link/customization/ |
| `/item/get` fields | `Item.Products`, `Item.BilledProducts` (mutually exclusive with `AvailableProducts`), `Item.ConsentedProducts` (consented scopes AND Production access), `Item.AvailableProducts`, `consented_data_scopes`, `consent_expiration_time` (`model_item.go`). | high | https://plaid.com/docs/api/items/ |

### 2.3 Transactions / sync semantics (`/transactions/sync`)

Go: `client.PlaidApi.TransactionsSync(ctx).TransactionsSyncRequest(req).Execute()` (`api_plaid.go` ~line 42803). Request `model_transactions_sync_request.go`: `Cursor *string` (omitempty), `Count *int32`, `Options *TransactionsSyncRequestOptions` (`IncludeOriginalDescription NullableBool`, `PersonalFinanceCategoryVersion *PersonalFinanceCategoryVersion` line 25, `DaysRequested *int32`, `AccountId *string`). Response `model_transactions_sync_response.go` (lines 19-33): exactly `TransactionsUpdateStatus`, `Accounts []AccountBase`, `Added []Transaction`, `Modified []Transaction`, `Removed []RemovedTransaction`, `NextCursor string`, `HasMore bool`, `RequestId string`.

| Fact | Confidence | Source |
|---|---|---|
| Pagination: while `has_more`, call again with `cursor = next_cursor`; always drain all pages in one job. | high | https://plaid.com/docs/api/products/transactions/#transactionssync |
| Cursor persistence: keep the ORIGINAL cursor of the first page in memory while paginating; persist only the `has_more=false` cursor, together with all accumulated added/modified/removed, in ONE DB transaction after the loop. Plaid's sample: `database.applyUpdates(itemId, added, modified, removed, cursor)` after the loop. | high | https://plaid.com/docs/transactions/#integration-overview ; https://plaid.com/docs/transactions/sync-migration/ |
| The `has_more=false` cursor is valid ≥ 1 year. `next_cursor` is `""` if transactions are not yet available. No validity guarantee documented for intermediate cursors. Max length 256 chars base64. | high | https://plaid.com/docs/api/products/transactions/#transactionssync |
| Any mid-pagination failure ⇒ restart the ENTIRE loop from the original cursor (API ref phrases this generally; `TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION` is the e.g.). Retrying only the failed page recurs the error. | high | https://plaid.com/docs/errors/transactions/#transactions_sync_mutation_during_pagination |
| `count`: default 100, min 1, max 500. Plaid recommends 500 to reduce pages and the mutation error. | high | same |
| First call: omit cursor (or `""`) ⇒ entire history as `added`. Commonly returns empty arrays and possibly `""` cursor within seconds of Item creation; this arms `SYNC_UPDATES_AVAILABLE`. `/transactions/sync` never returns `PRODUCT_NOT_READY`. First call once history is ready can have up to 8x latency — size HTTP timeouts. | high | https://plaid.com/docs/api/products/transactions/#transactionssync ; https://plaid.com/docs/transactions/sync-migration/ ; https://plaid.com/docs/transactions/troubleshooting/ |
| `cursor: "now"` returns only a forward cursor; supported ONLY for migrating Items from `/transactions/get`. `""` vs `"now"` differ in whether a pre-existing-but-later-modified transaction appears as `added` vs `modified`. Full rebuild: `""`, treat every row as upsert. | high | https://plaid.com/docs/transactions/sync-migration/ |
| `transactions_update_status` enum: `TRANSACTIONS_UPDATE_STATUS_UNKNOWN`, `NOT_READY`, `INITIAL_UPDATE_COMPLETE`, `HISTORICAL_UPDATE_COMPLETE`. Mirrors webhook info; usable to recover from missed webhooks. Go: `TRANSACTIONSUPDATESTATUS_*` in `model_transactions_update_status.go`. | high | https://plaid.com/docs/api/products/transactions/#transactionssync |
| `added`/`modified`/`removed` each ordered by ascending last-modified time; no cross-array ordering guarantee. | high | same |
| `accounts` in the response contains only accounts with transactions in that response — not the canonical account list (use `/accounts/get`). | high | same |
| Pending → posted = pending `transaction_id` in `removed` + NEW posted row in `added` with `pending_transaction_id`; may span pages within one update. `pending_transaction_id` null if no pending data or no match; name/amount may differ; some pending rows vanish without posting (holds). Go: `PendingTransactionId NullableString`. | high | https://plaid.com/docs/transactions/transactions-data/#pending-and-posted-transactions ; https://plaid.com/docs/transactions/webhooks/ |
| Posted rows are mutable (refunds, recategorization); pending details can change. `modified` handler must overwrite all mutable columns. Treat `added` and `modified` identically as upsert by `transaction_id`. | high | same |
| `RemovedTransaction` = `TransactionId string`, `AccountId string` only (`model_removed_transaction.go`). | high | Go client |
| Amount sign: positive = money out, negative = money in (purchases +, card payments/deposits/refunds −). Same for CIBC chequing and Amex credit. Go `Amount float64` — convert to fixed-point at ingest. | high | https://plaid.com/docs/api/products/transactions/#transactionssync |
| `iso_currency_code` / `unofficial_currency_code` mutually exclusive (both `NullableString`, `model_transaction.go` lines 25/27). CAD not promised. | high | same |
| Nullable: `authorized_date` (`NullableString`), `datetime`/`authorized_datetime` (`NullableTime`, select institutions, may be 00:00:00), `merchant_name` (`NullableString`), `personal_finance_category` (`NullablePersonalFinanceCategory`), `pending_transaction_id`. Non-nullable: `transaction_id`, `account_id`, `amount`, `date`, `name`, `pending`, `payment_channel` (`online`, `in store`, `other`). | high | same ; Go client |
| `date` = occurrence date (pending) / posted date (posted); prefer `COALESCE(authorized_date, date)` for display. Timezone of these dates undocumented. | high | same |
| PFC: `primary`, `detailed`, `confidence_level`. Customers enabled on/after 2025-12-03 get v2 only (a 2026 Trial team). Legacy `category`/`category_id` removed for customers enabled on/after 2025-05-05. `merchant_category_code` beta, mostly card transactions. `counterparties[]` (`name`, `entity_id`, `type`, `website`, `logo_url`, `confidence_level`, `account_numbers`). | high | https://plaid.com/docs/api/products/transactions/ ; https://plaid.com/docs/changelog/ |
| Fill rates: merchant_name 97% (excl. merchant-less), PFC 95%; no country breakdown. merchant_name introduced for US+Canada (June 2020); counterparties added Jan 2024; Enrich is US/CA only. | high (rates) / medium (CA applicability) | https://plaid.com/docs/transactions/ ; https://plaid.com/docs/changelog/ ; https://plaid.com/docs/enrich/ |
| `options.account_id` creates a separate cursor stream per account; never mix with Item-level cursors. | high | https://plaid.com/docs/api/products/transactions/#transactionssync |
| Refresh cadence: Plaid checks 1–4×/day per institution; `/transactions/refresh` is a paid add-on (in Trial bundle as "Transactions Refresh"). Sync supports credit, depository, and loan (student/mortgage) accounts. | high | same ; https://plaid.com/docs/account/billing/ |
| Rate limits — Production: 50/min per Item; 2,500/min per client (500/min for empty-cursor requests). Sandbox: 50/min per Item; 1,000/min per client (250 empty-cursor). Exceeding ⇒ HTTP 429 `RATE_LIMIT_EXCEEDED` / `TRANSACTIONS_SYNC_LIMIT`. Thresholds may differ per customer and change any time. | high | https://plaid.com/docs/errors/rate-limit-exceeded/#transactions_sync_limit |
| Pending-less institutions named in docs: Capital One, USAA. CIBC/Amex CA not mentioned. | high | https://plaid.com/docs/transactions/transactions-data/ |
| Recurring Transactions (`/transactions/recurring/get`): add-on in US/CA/UK at all Transactions institutions; needs a product access request; not in the Trial list; not listed under Canada in the support matrix. | high (availability) / unknown (Trial) | https://plaid.com/docs/transactions/ |

### 2.4 Webhooks + verification

Only what the input established; signature verification was not researched.

| Fact | Confidence | Source |
|---|---|---|
| `TRANSACTIONS: SYNC_UPDATES_AVAILABLE` — fires only after `/transactions/sync` has been called ≥1 on the Item. For Items not initialized with Transactions it fires twice (30-day initial, then full history). Payload: `initial_update_complete` (most recent 30 days) and `historical_update_complete` (full requested history, up to 2 years). Go: `SyncUpdatesAvailableWebhook.InitialUpdateComplete bool`, `HistoricalUpdateComplete bool` (`model_sync_updates_available_webhook.go`). | high | https://plaid.com/docs/api/products/transactions/#sync_updates_available ; https://plaid.com/docs/transactions/webhooks/ |
| Legacy `INITIAL_UPDATE`, `HISTORICAL_UPDATE`, `DEFAULT_UPDATE`, `TRANSACTIONS_REMOVED` still fire; not needed with `/transactions/sync`. Do not drive deletes from `TRANSACTIONS_REMOVED`. | high | same ; https://plaid.com/docs/transactions/sync-migration/ |
| `ITEM: PENDING_DISCONNECT` — sent 7 days before consent expiry for US/CA Items (`PENDING_EXPIRATION` is UK/EU). Fix: update mode. If not refreshed ⇒ `ITEM_LOGIN_REQUIRED`. `consent_expiration_time` on `/item/get`. | high | https://plaid.com/docs/link/oauth/ ; https://plaid.com/docs/link/update-mode/ |
| `ITEM_LOGIN_REQUIRED`, `LOGIN_REPAIRED`, `NEW_ACCOUNTS_AVAILABLE` are referenced in the input as the codes to route into update mode / account re-selection for Canadian Items. Their payload shapes were not researched. | medium (names only) | https://plaid.com/docs/link/update-mode/ (referenced) |
| `webhook` in `/link/token/create` is ignored in update mode; change via `/item/webhook/update`. | high | https://plaid.com/docs/api/link/ |
| After a repaired Item (error >24 h) expect an immediate transactions check ⇒ a `SYNC_UPDATES_AVAILABLE` shortly after update mode. | high | https://plaid.com/docs/link/update-mode/ |
| Webhook signature verification (`Plaid-Verification` JWT / `/webhook_verification_key/get`): **not in input.** | — | — |

### 2.5 Errors + retry classes

Only codes that appeared in the input. The general `error_type` taxonomy (API_ERROR, INSTITUTION_ERROR, ...) was not researched.

| Code | error_type / HTTP | Meaning | Handling | Confidence | Source |
|---|---|---|---|---|---|
| `TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION` | `TRANSACTIONS_ERROR` / 400 (Go: `PLAIDERRORTYPE_TRANSACTIONS_ERROR`) | Data changed mid-pagination | Discard in-memory patches, restart loop from original cursor, bounded retries | high | https://plaid.com/docs/errors/transactions/ |
| `TRANSACTIONS_SYNC_LIMIT` | `RATE_LIMIT_EXCEEDED` / 429 | Sync rate limit | Backoff + retry; client-wide limiter | high | https://plaid.com/docs/errors/rate-limit-exceeded/ |
| `TRIAL_CONNECTION_LIMIT` | `RATE_LIMIT_EXCEEDED` / 429 | 10-Item Trial cap hit (on Link/exchange) | NOT retryable; surface distinctly from sync 429s | high | https://plaid.com/docs/errors/rate-limit-exceeded/#trial_connection_limit |
| `ADDITIONAL_CONSENT_REQUIRED` | `INVALID_INPUT` / 400 (NOT `ITEM_ERROR`); message e.g. "client does not have user consent to access the PRODUCT_AUTH product" | Missing DTM consent for product (same code as not enabled) | Special-case: route to update mode with `additional_consented_products`; do not classify as programming bug | high | https://plaid.com/docs/errors/invalid-input/ ; DTM guide |
| `NO_LIABILITY_ACCOUNTS` | `ITEM_ERROR` / 400 | `/liabilities/get` with no valid liability accounts | Non-fatal for the Item: mark "no liabilities"; if persistent, Plaid says open a support ticket | high | https://plaid.com/docs/errors/item/ |
| `PRODUCTS_NOT_SUPPORTED` | `ITEM_ERROR` | Product not supported by the Item; check `/item/get` | Non-retryable for that product; disable that job for the Item | high | https://plaid.com/docs/errors/item/ |
| `ITEM_LOGIN_REQUIRED` | Item error state (also webhook) | Credentials/MFA/consent invalid or OAuth migration | Update mode (relink); expected more often at CIBC per KI123004 | high (state) / low (CIBC frequency) | https://plaid.com/docs/link/oauth/ ; Lunch Money KB |
| `PRODUCT_NOT_READY` | — | Never returned by `/transactions/sync` (empty arrays + `""` cursor instead) | Not an error; wait for webhook | high | https://plaid.com/docs/transactions/sync-migration/ |
| Empty response after link | — | Expected before initial pull | Keep Item in "pending initial pull" keyed on `transactions_update_status` | high | https://plaid.com/docs/transactions/troubleshooting/ |

### 2.6 Sandbox tooling

| Fact | Confidence | Source |
|---|---|---|
| Sandbox vs Production: DTM use case not required (required in Production if enrolled); http redirect URIs allowed (https required in Production; Sandbox http only for localhost). | high | https://plaid.com/docs/sandbox/ ; https://plaid.com/docs/link/oauth/ |
| Sandbox does not reflect institution-specific quirks: transaction history limits not enforced, pending transactions may appear where Production omits them, all OAuth institutions share one generic flow. Plaid recommends Trial (Production) testing afterwards. | high | https://plaid.com/docs/sandbox/ |
| Sandbox-only test institutions: `ins_43` Tartan-Dominion Bank of Canada (Canadian bank — exercises `country_codes ['CA']`, not CIBC/Amex); `ins_117650` Platypus OAuth Bank (OAuth testing); `ins_127287` First Platypus Bank - OAuth (consent expiration testing). | high | https://plaid.com/docs/sandbox/institutions/ |
| Sandbox rate limits for sync: 50/min per Item; 1,000/min per client (250 empty-cursor). | high | https://plaid.com/docs/errors/rate-limit-exceeded/ |
| All countries are enabled in Sandbox for US/CA teams. | high | https://plaid.com/docs/api/institutions/ |
| Sandbox webhook-firing endpoints (`/sandbox/item/fire_webhook`, `/sandbox/item/reset_login`, etc.) and Sandbox credentials/MFA test users: **not in input.** | — | — |

---

## 3. Recommended settings

### 3.1 `/link/token/create` — new Item (Canadian flow)

```go
req := plaid.NewLinkTokenCreateRequest(clientName, language /* "en" default, "fr" if requested */, []plaid.CountryCode{plaid.COUNTRYCODE_CA})
req.SetUser(plaid.LinkTokenCreateRequestUser{ClientUserId: userID})
req.SetProducts([]plaid.Products{plaid.PRODUCTS_TRANSACTIONS})                       // only this
req.SetAdditionalConsentedProducts([]plaid.Products{plaid.PRODUCTS_LIABILITIES})     // consent now, bill on first /liabilities/get
// RequiredIfSupportedProducts: leave nil (would subscribe Liabilities immediately; bills after Trial upgrade)
// OptionalProducts: leave nil (no billing advantage over required_if_supported for Liabilities)
req.SetTransactions(plaid.LinkTokenTransactions{DaysRequested: plaid.PtrInt32(730)}) // frozen per Item; cannot raise later without relink (burns a Trial slot)
req.SetWebhook(webhookURL)      // https; per-Item receiver, change later via /item/webhook/update
req.SetRedirectUri(redirectURI) // https in Production (http://localhost in Sandbox), no '?' or '#', no wildcard, allow-listed in Dashboard; nil for Android (use AndroidPackageName)
// Optional: req.SetLinkCustomizationName(name) — only if the customization is configured for country CA + the same language
```

- Do NOT put `auth`, `identity`, `liabilities`, or `statements` in `products`: Amex CA has `auth=0`, `identity=0`; `statements=0` at both; `liabilities` in `products` bills at creation and hides institutions lacking it. (https://plaid.com/docs/api/link/ ; CSV)
- Optionally add `PRODUCTS_IDENTITY` to `additional_consented_products` if `/identity/get` is wanted for CIBC; it will be unusable on Amex CA.
- Use `country_codes=['CA']` only (not `['US','CA']`) so the product set is not intersected with US enablement and the CA customization applies. (https://plaid.com/docs/api/link/)
- Before the first Production Link: select a DTM use case in Dashboard > Link > Link Customization. (https://plaid.com/docs/link/data-transparency-messaging-migration-guide/)
- Config validation: reject `redirect_uri` that is non-https outside Sandbox, or contains `?`, `#`, `*`.
- Frontend must persist the `link_token` across the OAuth redirect and re-init Link with the same token + `receivedRedirectUri = window.location.href`. (https://plaid.com/docs/link/oauth/)
- After exchange: call `/item/get`; record `institution_id`, `Item.ConsentedProducts`, `Item.Products`, `consent_expiration_time`; call `/accounts/get` for the canonical account list; call `/transactions/sync` once immediately (expect empty) to arm `SYNC_UPDATES_AVAILABLE`.
- Gate the liabilities job on `ConsentedProducts` containing `PRODUCTS_LIABILITIES` AND ≥1 account with `type=credit`; treat `NO_LIABILITY_ACCOUNTS` / `PRODUCTS_NOT_SUPPORTED` as "none for this Item".

### 3.2 `/link/token/create` — update mode

```go
req := plaid.NewLinkTokenCreateRequest(clientName, language, []plaid.CountryCode{plaid.COUNTRYCODE_CA})
req.SetUser(plaid.LinkTokenCreateRequestUser{ClientUserId: userID})
req.SetAccessToken(accessToken)
// Products: nil; Transactions: nil; Webhook: ignored in update mode
req.SetRedirectUri(redirectURI) // same URI as initial
upd := plaid.NewLinkTokenCreateRequestUpdate()
upd.SetAccountSelectionEnabled(true) // required for CA non-OAuth Items when adding/removing accounts (NEW_ACCOUNTS_AVAILABLE)
req.SetUpdate(*upd)
// Only when gathering new consent (ADDITIONAL_CONSENT_REQUIRED):
// req.SetAdditionalConsentedProducts([]plaid.Products{plaid.PRODUCTS_LIABILITIES})
// Never set Update.ReauthorizationEnabled (not currently used).
```

- Mint on demand (30-minute expiry); do not cache.
- On `onSuccess`: keep the existing `access_token` (unchanged); reconcile accounts from `/accounts/get`, archive de-selected ones. Expect a `SYNC_UPDATES_AVAILABLE` if the Item was in error >24 h.

### 3.3 `/transactions/sync` and the cursor persistence rule

- Request: `Count=500`; `Cursor` omitted (or `SetCursor("")`) for the first call and for full rebuilds; never `"now"`; never `Options.AccountId`; `Options.PersonalFinanceCategoryVersion = v2` explicitly; `Options.DaysRequested` not set (no effect).
- Loop: `orig := savedCursor`; accumulate `added`, `modified`, `removed` in memory across pages until `HasMore == false`.
- Commit rule: one DB transaction applying all patches AND storing `NextCursor` only after `HasMore == false`. Never persist an intermediate cursor. Cursor column: `VARCHAR(256)` / TEXT, empty string allowed (means "no cursor yet").
- Failure rule: any error on any page ⇒ discard accumulated patches, retry the whole loop from `orig` (bounded attempts; backoff on 429).
- Apply order: upsert `added` ∪ `modified` by `transaction_id` (overwrite all mutable columns: amount, name, merchant_name, PFC, date, authorized_date, pending, pending_transaction_id, currency); delete `removed` by `transaction_id` (idempotent no-op if unknown). To carry user data pending→posted, join `added.pending_transaction_id` → `removed.transaction_id` before deleting. Do not heuristic-match on (date, amount, name).
- Store `transactions_update_status` per Item; UI shows "loading history" until `HISTORICAL_UPDATE_COMPLETE`; use it as the missed-webhook fallback.
- Store currency = `COALESCE(iso_currency_code, unofficial_currency_code)`; amounts as fixed-point; Plaid's sign (out = +, in = −) is used unchanged; display date = `COALESCE(authorized_date, date)`.
- Scheduling: sync on every `SYNC_UPDATES_AVAILABLE` plus a low-frequency safety poll (e.g. daily); Plaid refreshes 1–4×/day. Client-wide limiter ≤2,500/min (Prod) with a separate ≤500/min budget for empty-cursor rebuilds (Sandbox: 1,000 / 250). Log `request_id` on every call.

### 3.4 Error code lists

- **Retryable (backoff, bounded):** `RATE_LIMIT_EXCEEDED` with `error_code=TRANSACTIONS_SYNC_LIMIT` (429); `TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION` (400, retry = restart loop from original cursor). Empty response / `""` cursor after link is not an error.
- **Needs re-auth / update mode:** `ITEM_LOGIN_REQUIRED` (error state / webhook); `PENDING_DISCONNECT` webhook (pre-emptive, 7 days ahead); `ADDITIONAL_CONSENT_REQUIRED` (`INVALID_INPUT`, 400 — must be special-cased before any generic INVALID_INPUT handling; fix = update mode with `additional_consented_products`).
- **Fatal / non-retryable:** `TRIAL_CONNECTION_LIMIT` (429 but permanent until paid upgrade); `PRODUCTS_NOT_SUPPORTED` (`ITEM_ERROR`, disable that product job for the Item); `NO_LIABILITY_ACCOUNTS` (`ITEM_ERROR`, disable liabilities job for the Item; Item itself stays healthy).
- **Unclassified (not in input):** all other `ITEM_ERROR`, `INSTITUTION_ERROR`, `API_ERROR`, `INVALID_REQUEST`, `INVALID_INPUT` codes — see Open questions.

### 3.5 Webhook codes to handle

| Type: code | Action |
|---|---|
| `TRANSACTIONS: SYNC_UPDATES_AVAILABLE` | Enqueue sync for `item_id`; optionally sync early when `initial_update_complete=true`. |
| `TRANSACTIONS: INITIAL_UPDATE`, `HISTORICAL_UPDATE`, `DEFAULT_UPDATE`, `TRANSACTIONS_REMOVED` | Acknowledge 200, ignore. |
| `ITEM: PENDING_DISCONNECT` | Flag Item "consent expiring"; prompt update mode. |
| `ITEM: ERROR` with `ITEM_LOGIN_REQUIRED` (payload shape not researched) | Flag Item "relink required"; prompt update mode. |
| `ITEM: LOGIN_REPAIRED`, `NEW_ACCOUNTS_AVAILABLE` (payload shapes not researched) | Clear relink flag / prompt update mode with `account_selection_enabled=true`. |

**Firing them in Sandbox:** not covered by the input. The Sandbox webhook-firing endpoint and its supported codes must be looked up in the Sandbox docs before implementing test hooks (Open question Q-S1).

---

## 4. Open questions

### Test in Sandbox / Trial (empirical)

- **Q-C1** `oauth` flag for `ins_37` and `ins_100533`: call `/institutions/get_by_id` with `country_codes ['CA']` (free) and record `Institution.Oauth` + `Institution.Products`; compare to CSV. Also pass `options.include_status=true` to see current health (KI123004).
- **Q-C2** Which product set is live for CIBC (CSV says liabilities/identity/identity_match/income_verification; marketing page says Assets/Auth/Balance/Transactions only)? Same call as Q-C1.
- **Q-C3** Amex CA supplementary cardholders / multiple cards: link a real Amex CA Trial Item, inspect `/accounts/get` (one account per card vs consolidated vs merged into primary).
- **Q-C4** Do CIBC and Amex CA deliver pending transactions (and pending→posted `removed`/`added` pairs)? Do they populate `datetime`/`authorized_datetime`? Is `iso_currency_code` always `CAD`, and how are foreign-currency Amex charges represented?
- **Q-C5** Actual transaction history depth returned for `days_requested=730` at CIBC and Amex CA (Sandbox does not enforce institution limits).
- **Q-C6** What `/liabilities/get` returns for a CIBC / Amex CA credit card: full credit object, partial/null fields, `NO_LIABILITY_ACCOUNTS`, `PRODUCTS_NOT_SUPPORTED`, or `ADDITIONAL_CONSENT_REQUIRED`? Test on a consented Item.
- **Q-C7** Fill rates of `merchant_name`, `counterparties`, `personal_finance_category` on real CIBC / Amex CA rows (no country breakdown documented).
- **Q-C8** Does update mode at a non-OAuth CA institution (to add liabilities consent or re-select accounts) force a full credential + MFA re-login, or is the "streamlined" flow used?
- **Q-C9** Does Amex CA render institution-controlled credential/MFA screens in French when `language='fr'`?
- **Q-C10** Does Amex CA / CIBC populate `consent_expiration_time` on `/item/get` (i.e. is there periodic consent expiry for CA Items)? Watch for `PENDING_DISCONNECT`.
- **Q-C11** Behaviour of a persisted intermediate cursor (from a `has_more=true` page) after a crash; error code returned for an expired/malformed cursor — neither is documented. Design already avoids relying on this; verify the failure mode anyway.
- **Q-C12** Can the same `transaction_id` appear in more than one of `added`/`modified`/`removed` within one update, and can `removed` reference an id never delivered? Undocumented; the upsert/idempotent-delete design tolerates both, but confirm.
- **Q-C13** Does `transactions_update_status = HISTORICAL_UPDATE_COMPLETE` on a `has_more=false` response guarantee the full history arrived in that sync, or can later `SYNC_UPDATES_AVAILABLE` still backfill?
- **Q-C14** Timezone of `date` / `authorized_date` (institution-local vs UTC) — not stated.
- **Q-S1** Sandbox webhook-firing endpoint and supported webhook codes (e.g. `SYNC_UPDATES_AVAILABLE`, `PENDING_DISCONNECT`, `ITEM_LOGIN_REQUIRED`) — not in input; look up in Sandbox docs and confirm which codes can be simulated, including whether `ins_43` Tartan-Dominion Bank of Canada supports them.
- **Q-S2** Webhook signature verification (`Plaid-Verification` JWT, `/webhook_verification_key/get`, key caching rules) — not in input; look up before implementing the receiver.
- **Q-S3** General error taxonomy (`API_ERROR`, `INSTITUTION_ERROR`, `INSTITUTION_DOWN`, `INSTITUTION_NOT_RESPONDING`, `INVALID_REQUEST`, other `ITEM_ERROR` codes) and which are retryable — not in input; needed for the generic classifier.
- **Q-S4** Whether Trial-plan clients get the published Production rate limits (docs say thresholds may differ per customer).

### Ask Plaid support / check Dashboard

- **Q-P1** Is CIBC / Amex CA reachable on the Trial plan ("almost all institutions" — exclusions not enumerated)?
- **Q-P2** Does a Trial team enabled for CA need a product-access ticket for `country_codes ['CA']` in Production? Institutions reference says no for US/CA teams; Link reference wording is ambiguous.
- **Q-P3** Is this team DTM-enrolled (expected yes, created after 2024-10-31)? Which Link customization carries the DTM use case, and is its country/language config exactly `CA` + the language sent?
- **Q-P4** Can a Canada-only Trial customer opt out of DTM without an account manager? (Not needed if `additional_consented_products` flow is adopted.)
- **Q-P5** Does the Trial questionnaire capture a product list, and must Liabilities be selected there for Trial Items' liabilities consent/data to survive an upgrade ("you will only retain Production access to any products you explicitly requested in your Production request form")?
- **Q-P6** Is Recurring Transactions (`/transactions/recurring/get`) obtainable on Trial, and is it available for Canadian institutions (support matrix does not list it under Canada)?
- **Q-P7** Do update-mode sessions count against the 10-Item cap? Docs imply no (`access_token` unchanged; "access tokens created ... count") but do not say so explicitly.
- **Q-P8** Current status of Known Issue KI123004 (CIBC/Simplii MFA invalidation) — Dashboard Institutions status page for `ins_37`.
- **Q-P9** Any timeline for CIBC / Amex CA OAuth or open-banking migration, and whether a new `institution_id` would be issued — Dashboard "migrations" pane.
- **Q-P10** Trial eligibility / approval time for a Canadian-resident individual or sole proprietor ("auto-approved for most developers" — unspecified).
- **Q-P11** Post-upgrade billing: if `/liabilities/get` succeeds once then later fails on an Item, does the Liabilities subscription persist (billing page says charges continue even when calls fail)?
- **Q-P12** Coverage CSV freshness: in-file stamp 2026-08-12 vs HTTP Last-Modified 2026-09-09 — treat `/institutions/get_by_id` as authoritative regardless.
