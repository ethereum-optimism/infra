# PR Review: API Key Rate Limit Exemption

## Summary
This PR adds functionality to bypass rate limiting for requests that include a valid API key in the `X-Api-Key` header. API keys are configured via the `API_KEYS` environment variable as a comma-separated list.

---

## 🔴 CRITICAL ISSUES

### 1. **Missing Whitespace Trimming in API Key Parsing**
**Location:** `proxyd.go:423`
```go
apiKeys = append(apiKeys, strings.Split(keys, ",")...)
```

**Issue:** When splitting by comma, whitespace around keys is not trimmed. This means:
- `API_KEYS="key1, key2, key3"` results in `["key1", " key2", " key3"]`
- `API_KEYS="key1,key2"` works, but `API_KEYS="key1, key2"` won't match properly
- Users will experience confusing failures when they include spaces (common mistake)

**Fix:**
```go
keysList := strings.Split(keys, ",")
for _, key := range keysList {
    trimmed := strings.TrimSpace(key)
    if trimmed != "" {
        apiKeys = append(apiKeys, trimmed)
    }
}
```

**Impact:** High - Will cause legitimate API keys to fail validation if users include spaces.

---

## 🟡 MEDIUM PRIORITY ISSUES

### 2. **Empty Strings Not Filtered**
**Location:** `proxyd.go:423`

**Issue:** If `API_KEYS` contains empty values (e.g., `"key1,,key2"` or trailing comma `"key1,key2,"`), empty strings will be added to the exempt keys list. While `isValidAPIKey` won't match empty strings unless explicitly added, it's still wasteful and could cause confusion.

**Fix:** Filter empty strings as shown in fix #1 above.

**Impact:** Medium - Code cleanliness and potential confusion.

### 3. **Silent Error Handling**
**Location:** `proxyd.go:422`
```go
if keys, err := ReadFromEnvOrConfig("$API_KEYS"); err == nil {
    apiKeys = append(apiKeys, strings.Split(keys, ",")...)
}
```

**Issue:** If the `API_KEYS` environment variable doesn't exist, the error is silently ignored and the feature is disabled. While this might be intentional (optional feature), it could hide misconfiguration issues.

**Recommendation:** Consider logging a debug message when the env var is not found, or document this behavior clearly.

**Impact:** Medium - Could hide configuration issues, but might be intentional.

---

## 🟢 LOW PRIORITY ISSUES / SUGGESTIONS

### 4. **Incomplete Test Coverage**
**Location:** `integration_tests/rate_limit_test.go:60-74`

**Issues:**
- Only tests first and last keys, not middle keys (e.g., `"qrs"` in `"hijklmnop,qrs,tuv"`)
- Doesn't test edge cases:
  - Empty string in header
  - Invalid keys
  - Keys with whitespace (if fix #1 is applied, test should verify trimming works)
  - Case sensitivity (should verify if keys are case-sensitive)

**Recommendation:** Add test cases for middle keys and edge cases.

**Impact:** Low - Feature works but test coverage could be better.

### 5. **Missing Documentation**
**Issue:** No comments or documentation explaining:
- What the feature does
- How to configure it
- Security considerations
- Whether keys are case-sensitive

**Recommendation:** Add godoc comments and update README if applicable.

**Impact:** Low - Documentation/developer experience.

### 6. **WebSocket Support**
**Location:** `server.go:700` (`HandleWS`)

**Observation:** WebSocket connections don't appear to support API key bypass. This might be intentional (WebSockets may not have rate limiting), but should be documented or verified.

**Impact:** Low - May be intentional, but worth clarifying.

---

## ✅ POSITIVE ASPECTS

1. **Security:** Uses `crypto/subtle.ConstantTimeCompare` for key comparison - excellent for preventing timing attacks
2. **Consistent Implementation:** Bypass logic is consistently applied to both frontend rate limiting and sender rate limiting
3. **Test Coverage:** Basic functionality is tested
4. **Code Structure:** Clean separation of concerns

---

## 📋 RECOMMENDED ACTIONS

### Must Fix (Before Merge):
1. ✅ Add whitespace trimming when parsing API keys
2. ✅ Filter empty strings from the API keys list

### Should Fix (Before Merge):
3. ⚠️ Add test for middle key in the list
4. ⚠️ Consider logging when API_KEYS env var is not found (or document the silent behavior)

### Nice to Have:
5. 📝 Add documentation/comments
6. 📝 Expand test coverage for edge cases

---

## 🔍 CODE QUALITY NOTES

- The `bypassLimit` parameter is correctly threaded through the call chain
- The implementation correctly bypasses both frontend and sender rate limits
- The use of constant-time comparison is a security best practice

---

## ⚠️ SECURITY CONSIDERATIONS

1. **API Key Storage:** API keys are stored in memory as plain strings. Consider:
   - Logging: Ensure API keys are not logged (currently appears safe)
   - Rotation: Document how to rotate keys (restart required)
   - Leakage: Keys are in environment variables - ensure proper access controls

2. **Header Name:** `X-Api-Key` is a common header name. Consider if this conflicts with other systems or if a custom header would be better.

3. **Rate Limit Bypass:** This completely bypasses rate limiting. Ensure this is the intended behavior and that there are other protections in place (e.g., authentication, IP-based limits, etc.).
