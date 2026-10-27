# OVH API token for DNS-01 (least privilege)

One DNS profile per OVH **account**. A profile can manage several zones of the
same account (e.g. your domain and your friend's domain hosted in your account).

1. Open the token page of your endpoint, e.g. https://eu.api.ovh.com/createToken/
   (`ovh-ca`: https://ca.api.ovh.com/createToken/, `ovh-us`: https://api.us.ovhcloud.com/createToken/).
2. Fill in your OVH login, a name (*Marineris DNS-01*) and a validity
   (*Unlimited* is acceptable for a restricted token; otherwise plan rotation).
3. Rights — add exactly these lines **for every zone** of the profile, replacing
   `example.com`:

   | Method | Path |
   |---|---|
   | GET | `/domain/zone/example.com/*` |
   | POST | `/domain/zone/example.com/*` |
   | PUT | `/domain/zone/example.com/*` |
   | DELETE | `/domain/zone/example.com/*` |

   Do **not** add `/domain/*`, `/me/*` or `*`.
4. Optional: restrict the token to your server's public IP address.
5. In Marineris, **DNS profiles → New profile**: provider *OVHcloud*, endpoint,
   application key, application secret, consumer key.
6. Press **Test** with each zone: the steps must show the TXT record created,
   visible and deleted.

**Verify:** a test with a zone that is *not* in the token's rights fails with
an authorization error — proof that the token is scoped.
