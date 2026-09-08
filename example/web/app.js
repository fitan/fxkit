// node_modules/map-obj/index.js
var isObject = (value) => typeof value === "object" && value !== null;
var isObjectCustom = (value) => isObject(value) && !(value instanceof RegExp) && !(value instanceof Error) && !(value instanceof Date);
var mapObjectSkip = Symbol("mapObjectSkip");
var _mapObject = (object, mapper, options, isSeen = /* @__PURE__ */ new WeakMap()) => {
  options = {
    deep: false,
    target: {},
    ...options
  };
  if (isSeen.has(object)) {
    return isSeen.get(object);
  }
  isSeen.set(object, options.target);
  const { target } = options;
  delete options.target;
  const mapArray = (array) => array.map((element) => isObjectCustom(element) ? _mapObject(element, mapper, options, isSeen) : element);
  if (Array.isArray(object)) {
    return mapArray(object);
  }
  for (const [key, value] of Object.entries(object)) {
    const mapResult = mapper(key, value, object);
    if (mapResult === mapObjectSkip) {
      continue;
    }
    let [newKey, newValue, { shouldRecurse = true } = {}] = mapResult;
    if (newKey === "__proto__") {
      continue;
    }
    if (options.deep && shouldRecurse && isObjectCustom(newValue)) {
      newValue = Array.isArray(newValue) ? mapArray(newValue) : _mapObject(newValue, mapper, options, isSeen);
    }
    target[newKey] = newValue;
  }
  return target;
};
function mapObject(object, mapper, options) {
  if (!isObject(object)) {
    throw new TypeError(`Expected an object, got \`${object}\` (${typeof object})`);
  }
  return _mapObject(object, mapper, options);
}

// node_modules/camelcase/index.js
var UPPERCASE = /[\p{Lu}]/u;
var LOWERCASE = /[\p{Ll}]/u;
var LEADING_CAPITAL = /^[\p{Lu}](?![\p{Lu}])/gu;
var IDENTIFIER = /([\p{Alpha}\p{N}_]|$)/u;
var SEPARATORS = /[_.\- ]+/;
var LEADING_SEPARATORS = new RegExp("^" + SEPARATORS.source);
var SEPARATORS_AND_IDENTIFIER = new RegExp(SEPARATORS.source + IDENTIFIER.source, "gu");
var NUMBERS_AND_IDENTIFIER = new RegExp("\\d+" + IDENTIFIER.source, "gu");
var preserveCamelCase = (string, toLowerCase, toUpperCase, preserveConsecutiveUppercase2) => {
  let isLastCharLower = false;
  let isLastCharUpper = false;
  let isLastLastCharUpper = false;
  let isLastLastCharPreserved = false;
  for (let index = 0; index < string.length; index++) {
    const character = string[index];
    isLastLastCharPreserved = index > 2 ? string[index - 3] === "-" : true;
    if (isLastCharLower && UPPERCASE.test(character)) {
      string = string.slice(0, index) + "-" + string.slice(index);
      isLastCharLower = false;
      isLastLastCharUpper = isLastCharUpper;
      isLastCharUpper = true;
      index++;
    } else if (isLastCharUpper && isLastLastCharUpper && LOWERCASE.test(character) && (!isLastLastCharPreserved || preserveConsecutiveUppercase2)) {
      string = string.slice(0, index - 1) + "-" + string.slice(index - 1);
      isLastLastCharUpper = isLastCharUpper;
      isLastCharUpper = false;
      isLastCharLower = true;
    } else {
      isLastCharLower = toLowerCase(character) === character && toUpperCase(character) !== character;
      isLastLastCharUpper = isLastCharUpper;
      isLastCharUpper = toUpperCase(character) === character && toLowerCase(character) !== character;
    }
  }
  return string;
};
var preserveConsecutiveUppercase = (input, toLowerCase) => {
  LEADING_CAPITAL.lastIndex = 0;
  return input.replaceAll(LEADING_CAPITAL, (match) => toLowerCase(match));
};
var postProcess = (input, toUpperCase) => {
  SEPARATORS_AND_IDENTIFIER.lastIndex = 0;
  NUMBERS_AND_IDENTIFIER.lastIndex = 0;
  return input.replaceAll(NUMBERS_AND_IDENTIFIER, (match, pattern, offset) => ["_", "-"].includes(input.charAt(offset + match.length)) ? match : toUpperCase(match)).replaceAll(SEPARATORS_AND_IDENTIFIER, (_, identifier) => toUpperCase(identifier));
};
function camelCase(input, options) {
  if (!(typeof input === "string" || Array.isArray(input))) {
    throw new TypeError("Expected the input to be `string | string[]`");
  }
  options = {
    pascalCase: false,
    preserveConsecutiveUppercase: false,
    ...options
  };
  if (Array.isArray(input)) {
    input = input.map((x) => x.trim()).filter((x) => x.length).join("-");
  } else {
    input = input.trim();
  }
  if (input.length === 0) {
    return "";
  }
  const toLowerCase = options.locale === false ? (string) => string.toLowerCase() : (string) => string.toLocaleLowerCase(options.locale);
  const toUpperCase = options.locale === false ? (string) => string.toUpperCase() : (string) => string.toLocaleUpperCase(options.locale);
  if (input.length === 1) {
    if (SEPARATORS.test(input)) {
      return "";
    }
    return options.pascalCase ? toUpperCase(input) : toLowerCase(input);
  }
  const hasUpperCase = input !== toLowerCase(input);
  if (hasUpperCase) {
    input = preserveCamelCase(input, toLowerCase, toUpperCase, options.preserveConsecutiveUppercase);
  }
  input = input.replace(LEADING_SEPARATORS, "");
  input = options.preserveConsecutiveUppercase ? preserveConsecutiveUppercase(input, toLowerCase) : toLowerCase(input);
  if (options.pascalCase) {
    input = toUpperCase(input.charAt(0)) + input.slice(1);
  }
  return postProcess(input, toUpperCase);
}

// node_modules/quick-lru/index.js
var QuickLRU = class extends Map {
  constructor(options = {}) {
    super();
    if (!(options.maxSize && options.maxSize > 0)) {
      throw new TypeError("`maxSize` must be a number greater than 0");
    }
    if (typeof options.maxAge === "number" && options.maxAge === 0) {
      throw new TypeError("`maxAge` must be a number greater than 0");
    }
    this.maxSize = options.maxSize;
    this.maxAge = options.maxAge || Number.POSITIVE_INFINITY;
    this.onEviction = options.onEviction;
    this.cache = /* @__PURE__ */ new Map();
    this.oldCache = /* @__PURE__ */ new Map();
    this._size = 0;
  }
  // TODO: Use private class methods when targeting Node.js 16.
  _emitEvictions(cache2) {
    if (typeof this.onEviction !== "function") {
      return;
    }
    for (const [key, item] of cache2) {
      this.onEviction(key, item.value);
    }
  }
  _deleteIfExpired(key, item) {
    if (typeof item.expiry === "number" && item.expiry <= Date.now()) {
      if (typeof this.onEviction === "function") {
        this.onEviction(key, item.value);
      }
      return this.delete(key);
    }
    return false;
  }
  _getOrDeleteIfExpired(key, item) {
    const deleted = this._deleteIfExpired(key, item);
    if (deleted === false) {
      return item.value;
    }
  }
  _getItemValue(key, item) {
    return item.expiry ? this._getOrDeleteIfExpired(key, item) : item.value;
  }
  _peek(key, cache2) {
    const item = cache2.get(key);
    return this._getItemValue(key, item);
  }
  _set(key, value) {
    this.cache.set(key, value);
    this._size++;
    if (this._size >= this.maxSize) {
      this._size = 0;
      this._emitEvictions(this.oldCache);
      this.oldCache = this.cache;
      this.cache = /* @__PURE__ */ new Map();
    }
  }
  _moveToRecent(key, item) {
    this.oldCache.delete(key);
    this._set(key, item);
  }
  *_entriesAscending() {
    for (const item of this.oldCache) {
      const [key, value] = item;
      if (!this.cache.has(key)) {
        const deleted = this._deleteIfExpired(key, value);
        if (deleted === false) {
          yield item;
        }
      }
    }
    for (const item of this.cache) {
      const [key, value] = item;
      const deleted = this._deleteIfExpired(key, value);
      if (deleted === false) {
        yield item;
      }
    }
  }
  get(key) {
    if (this.cache.has(key)) {
      const item = this.cache.get(key);
      return this._getItemValue(key, item);
    }
    if (this.oldCache.has(key)) {
      const item = this.oldCache.get(key);
      if (this._deleteIfExpired(key, item) === false) {
        this._moveToRecent(key, item);
        return item.value;
      }
    }
  }
  set(key, value, { maxAge = this.maxAge } = {}) {
    const expiry = typeof maxAge === "number" && maxAge !== Number.POSITIVE_INFINITY ? Date.now() + maxAge : void 0;
    if (this.cache.has(key)) {
      this.cache.set(key, {
        value,
        expiry
      });
    } else {
      this._set(key, { value, expiry });
    }
    return this;
  }
  has(key) {
    if (this.cache.has(key)) {
      return !this._deleteIfExpired(key, this.cache.get(key));
    }
    if (this.oldCache.has(key)) {
      return !this._deleteIfExpired(key, this.oldCache.get(key));
    }
    return false;
  }
  peek(key) {
    if (this.cache.has(key)) {
      return this._peek(key, this.cache);
    }
    if (this.oldCache.has(key)) {
      return this._peek(key, this.oldCache);
    }
  }
  delete(key) {
    const deleted = this.cache.delete(key);
    if (deleted) {
      this._size--;
    }
    return this.oldCache.delete(key) || deleted;
  }
  clear() {
    this.cache.clear();
    this.oldCache.clear();
    this._size = 0;
  }
  resize(newSize) {
    if (!(newSize && newSize > 0)) {
      throw new TypeError("`maxSize` must be a number greater than 0");
    }
    const items = [...this._entriesAscending()];
    const removeCount = items.length - newSize;
    if (removeCount < 0) {
      this.cache = new Map(items);
      this.oldCache = /* @__PURE__ */ new Map();
      this._size = items.length;
    } else {
      if (removeCount > 0) {
        this._emitEvictions(items.slice(0, removeCount));
      }
      this.oldCache = new Map(items.slice(removeCount));
      this.cache = /* @__PURE__ */ new Map();
      this._size = 0;
    }
    this.maxSize = newSize;
  }
  *keys() {
    for (const [key] of this) {
      yield key;
    }
  }
  *values() {
    for (const [, value] of this) {
      yield value;
    }
  }
  *[Symbol.iterator]() {
    for (const item of this.cache) {
      const [key, value] = item;
      const deleted = this._deleteIfExpired(key, value);
      if (deleted === false) {
        yield [key, value.value];
      }
    }
    for (const item of this.oldCache) {
      const [key, value] = item;
      if (!this.cache.has(key)) {
        const deleted = this._deleteIfExpired(key, value);
        if (deleted === false) {
          yield [key, value.value];
        }
      }
    }
  }
  *entriesDescending() {
    let items = [...this.cache];
    for (let i = items.length - 1; i >= 0; --i) {
      const item = items[i];
      const [key, value] = item;
      const deleted = this._deleteIfExpired(key, value);
      if (deleted === false) {
        yield [key, value.value];
      }
    }
    items = [...this.oldCache];
    for (let i = items.length - 1; i >= 0; --i) {
      const item = items[i];
      const [key, value] = item;
      if (!this.cache.has(key)) {
        const deleted = this._deleteIfExpired(key, value);
        if (deleted === false) {
          yield [key, value.value];
        }
      }
    }
  }
  *entriesAscending() {
    for (const [key, value] of this._entriesAscending()) {
      yield [key, value.value];
    }
  }
  get size() {
    if (!this._size) {
      return this.oldCache.size;
    }
    let oldCacheSize = 0;
    for (const key of this.oldCache.keys()) {
      if (!this.cache.has(key)) {
        oldCacheSize++;
      }
    }
    return Math.min(this._size + oldCacheSize, this.maxSize);
  }
  entries() {
    return this.entriesAscending();
  }
  forEach(callbackFunction, thisArgument = this) {
    for (const [key, value] of this.entriesAscending()) {
      callbackFunction.call(thisArgument, value, key, this);
    }
  }
  get [Symbol.toStringTag]() {
    return JSON.stringify([...this.entriesAscending()]);
  }
};

// node_modules/camelcase-keys/index.js
var has = (array, key) => array.some((element) => {
  if (typeof element === "string") {
    return element === key;
  }
  element.lastIndex = 0;
  return element.test(key);
});
var cache = new QuickLRU({ maxSize: 1e5 });
var isObject2 = (value) => typeof value === "object" && value !== null && !(value instanceof RegExp) && !(value instanceof Error) && !(value instanceof Date);
var transform = (input, options = {}) => {
  if (!isObject2(input)) {
    return input;
  }
  const {
    exclude,
    pascalCase = false,
    stopPaths,
    deep = false,
    preserveConsecutiveUppercase: preserveConsecutiveUppercase2 = false
  } = options;
  const stopPathsSet = new Set(stopPaths);
  const makeMapper = (parentPath) => (key, value) => {
    if (deep && isObject2(value)) {
      const path = parentPath === void 0 ? key : `${parentPath}.${key}`;
      if (!stopPathsSet.has(path)) {
        value = mapObject(value, makeMapper(path));
      }
    }
    if (!(exclude && has(exclude, key))) {
      const cacheKey = pascalCase ? `${key}_` : key;
      if (cache.has(cacheKey)) {
        key = cache.get(cacheKey);
      } else {
        const returnValue = camelCase(key, { pascalCase, locale: false, preserveConsecutiveUppercase: preserveConsecutiveUppercase2 });
        if (key.length < 100) {
          cache.set(cacheKey, returnValue);
        }
        key = returnValue;
      }
    }
    return [key, value];
  };
  return mapObject(input, makeMapper(void 0));
};
function camelcaseKeys(input, options) {
  if (Array.isArray(input)) {
    return Object.keys(input).map((key) => transform(input[key], options));
  }
  return transform(input, options);
}

// node_modules/@logto/js/lib/consts/openid.js
var ReservedScope;
(function(ReservedScope2) {
  ReservedScope2["OpenId"] = "openid";
  ReservedScope2["OfflineAccess"] = "offline_access";
})(ReservedScope || (ReservedScope = {}));
var ReservedResource;
(function(ReservedResource2) {
  ReservedResource2["Organization"] = "urn:logto:resource:organizations";
})(ReservedResource || (ReservedResource = {}));
var UserScope;
(function(UserScope2) {
  UserScope2["Profile"] = "profile";
  UserScope2["Email"] = "email";
  UserScope2["Phone"] = "phone";
  UserScope2["Address"] = "address";
  UserScope2["CustomData"] = "custom_data";
  UserScope2["Identities"] = "identities";
  UserScope2["Roles"] = "roles";
  UserScope2["Organizations"] = "urn:logto:scope:organizations";
  UserScope2["OrganizationRoles"] = "urn:logto:scope:organization_roles";
  UserScope2["Sessions"] = "urn:logto:scope:sessions";
})(UserScope || (UserScope = {}));
var idTokenClaims = Object.freeze({
  [UserScope.Profile]: ["name", "picture", "username"],
  [UserScope.Email]: ["email", "email_verified"],
  [UserScope.Phone]: ["phone_number", "phone_number_verified"],
  [UserScope.Address]: [],
  [UserScope.Roles]: ["roles"],
  [UserScope.Organizations]: ["organizations"],
  [UserScope.OrganizationRoles]: ["organization_roles"],
  [UserScope.CustomData]: [],
  [UserScope.Identities]: [],
  [UserScope.Sessions]: []
});
var userinfoClaims = Object.freeze({
  [UserScope.Profile]: [],
  [UserScope.Email]: [],
  [UserScope.Phone]: [],
  [UserScope.Address]: [],
  [UserScope.Roles]: [],
  [UserScope.Organizations]: [],
  [UserScope.OrganizationRoles]: [],
  [UserScope.CustomData]: ["custom_data"],
  [UserScope.Identities]: ["identities"],
  [UserScope.Sessions]: []
});
var userClaims = Object.freeze(
  // Hard to infer type directly, use `as` for a workaround.
  // eslint-disable-next-line no-restricted-syntax
  Object.fromEntries(Object.values(UserScope).map((current) => [
    current,
    [...idTokenClaims[current], ...userinfoClaims[current]]
  ]))
);

// node_modules/@logto/js/lib/consts/index.js
var ContentType = {
  formUrlEncoded: { "Content-Type": "application/x-www-form-urlencoded" }
};
var TokenGrantType;
(function(TokenGrantType2) {
  TokenGrantType2["AuthorizationCode"] = "authorization_code";
  TokenGrantType2["RefreshToken"] = "refresh_token";
})(TokenGrantType || (TokenGrantType = {}));
var QueryKey;
(function(QueryKey2) {
  QueryKey2["ClientId"] = "client_id";
  QueryKey2["Code"] = "code";
  QueryKey2["CodeChallenge"] = "code_challenge";
  QueryKey2["CodeChallengeMethod"] = "code_challenge_method";
  QueryKey2["CodeVerifier"] = "code_verifier";
  QueryKey2["Error"] = "error";
  QueryKey2["ErrorDescription"] = "error_description";
  QueryKey2["GrantType"] = "grant_type";
  QueryKey2["IdToken"] = "id_token";
  QueryKey2["IdTokenHint"] = "id_token_hint";
  QueryKey2["LoginHint"] = "login_hint";
  QueryKey2["PostLogoutRedirectUri"] = "post_logout_redirect_uri";
  QueryKey2["Prompt"] = "prompt";
  QueryKey2["RedirectUri"] = "redirect_uri";
  QueryKey2["RefreshToken"] = "refresh_token";
  QueryKey2["Resource"] = "resource";
  QueryKey2["ResponseType"] = "response_type";
  QueryKey2["Scope"] = "scope";
  QueryKey2["State"] = "state";
  QueryKey2["Token"] = "token";
  QueryKey2["InteractionMode"] = "interaction_mode";
  QueryKey2["OrganizationId"] = "organization_id";
  QueryKey2["FirstScreen"] = "first_screen";
  QueryKey2["Identifier"] = "identifier";
  QueryKey2["DirectSignIn"] = "direct_sign_in";
  QueryKey2["OneTimeToken"] = "one_time_token";
})(QueryKey || (QueryKey = {}));
var Prompt;
(function(Prompt2) {
  Prompt2["None"] = "none";
  Prompt2["Consent"] = "consent";
  Prompt2["Login"] = "login";
})(Prompt || (Prompt = {}));

// node_modules/@logto/js/lib/core/fetch-token.js
var fetchTokenByAuthorizationCode = async ({ clientId, tokenEndpoint, redirectUri: redirectUri2, codeVerifier, code, resource }, requester) => {
  const parameters = new URLSearchParams();
  parameters.append(QueryKey.ClientId, clientId);
  parameters.append(QueryKey.Code, code);
  parameters.append(QueryKey.CodeVerifier, codeVerifier);
  parameters.append(QueryKey.RedirectUri, redirectUri2);
  parameters.append(QueryKey.GrantType, TokenGrantType.AuthorizationCode);
  if (resource) {
    parameters.append(QueryKey.Resource, resource);
  }
  const snakeCaseCodeTokenResponse = await requester(tokenEndpoint, {
    method: "POST",
    headers: ContentType.formUrlEncoded,
    body: parameters.toString()
  });
  return camelcaseKeys(snakeCaseCodeTokenResponse);
};
var fetchTokenByRefreshToken = async (params, requester) => {
  const { clientId, tokenEndpoint, refreshToken, resource, organizationId, scopes } = params;
  const parameters = new URLSearchParams();
  parameters.append(QueryKey.ClientId, clientId);
  parameters.append(QueryKey.RefreshToken, refreshToken);
  parameters.append(QueryKey.GrantType, TokenGrantType.RefreshToken);
  if (resource) {
    parameters.append(QueryKey.Resource, resource);
  }
  if (organizationId) {
    parameters.append(QueryKey.OrganizationId, organizationId);
  }
  if (scopes?.length) {
    parameters.append(QueryKey.Scope, scopes.join(" "));
  }
  const snakeCaseRefreshTokenTokenResponse = await requester(tokenEndpoint, {
    method: "POST",
    headers: ContentType.formUrlEncoded,
    body: parameters.toString()
  });
  return camelcaseKeys(snakeCaseRefreshTokenTokenResponse);
};

// node_modules/@logto/js/lib/core/oidc-config.js
var discoveryPath = "/oidc/.well-known/openid-configuration";
var fetchOidcConfig = async (endpoint, requester) => camelcaseKeys(await requester(endpoint));

// node_modules/@logto/js/lib/core/revoke.js
var revoke = async (revocationEndpoint, clientId, token, requester) => requester(revocationEndpoint, {
  method: "POST",
  headers: ContentType.formUrlEncoded,
  body: new URLSearchParams({
    [QueryKey.ClientId]: clientId,
    [QueryKey.Token]: token
  }).toString()
});

// node_modules/@logto/js/lib/utils/scopes.js
var withReservedScopes = (originalScopes) => {
  const reservedScopes = Object.values(ReservedScope);
  const uniqueScopes = /* @__PURE__ */ new Set([...reservedScopes, UserScope.Profile, ...originalScopes ?? []]);
  return Array.from(uniqueScopes).join(" ");
};

// node_modules/@logto/js/lib/core/sign-in.js
var codeChallengeMethod = "S256";
var responseType = "code";
var buildPrompt = (prompt) => {
  if (Array.isArray(prompt)) {
    return prompt.join(" ");
  }
  return prompt ?? Prompt.Consent;
};
var generateSignInUri = ({ authorizationEndpoint, clientId, redirectUri: redirectUri2, codeChallenge, state, scopes, resources, prompt, firstScreen, identifiers: identifier, interactionMode, loginHint, directSignIn, oneTimeToken, extraParams, includeReservedScopes = true }) => {
  const urlSearchParameters = new URLSearchParams({
    [QueryKey.ClientId]: clientId,
    [QueryKey.RedirectUri]: redirectUri2,
    [QueryKey.CodeChallenge]: codeChallenge,
    [QueryKey.CodeChallengeMethod]: codeChallengeMethod,
    [QueryKey.State]: state,
    [QueryKey.ResponseType]: responseType,
    [QueryKey.Prompt]: buildPrompt(prompt)
  });
  const computedScopes = includeReservedScopes ? withReservedScopes(scopes) : scopes?.join(" ");
  if (computedScopes) {
    urlSearchParameters.append(QueryKey.Scope, computedScopes);
  }
  if (loginHint) {
    urlSearchParameters.append(QueryKey.LoginHint, loginHint);
  }
  if (directSignIn) {
    urlSearchParameters.append(QueryKey.DirectSignIn, `${directSignIn.method}:${directSignIn.target}`);
  }
  for (const resource of resources ?? []) {
    urlSearchParameters.append(QueryKey.Resource, resource);
  }
  if (firstScreen) {
    urlSearchParameters.append(QueryKey.FirstScreen, firstScreen);
  } else if (interactionMode) {
    urlSearchParameters.append(QueryKey.InteractionMode, interactionMode);
  }
  if (identifier && identifier.length > 0) {
    urlSearchParameters.append(QueryKey.Identifier, identifier.join(" "));
  }
  if (oneTimeToken) {
    urlSearchParameters.append(QueryKey.OneTimeToken, oneTimeToken);
  }
  if (extraParams) {
    for (const [key, value] of Object.entries(extraParams)) {
      urlSearchParameters.append(key, value);
    }
  }
  return `${authorizationEndpoint}?${urlSearchParameters.toString()}`;
};

// node_modules/@logto/js/lib/core/sign-out.js
var generateSignOutUri = ({ endSessionEndpoint, clientId, postLogoutRedirectUri: postLogoutRedirectUri2 }) => {
  const urlSearchParameters = new URLSearchParams({ [QueryKey.ClientId]: clientId });
  if (postLogoutRedirectUri2) {
    urlSearchParameters.append(QueryKey.PostLogoutRedirectUri, postLogoutRedirectUri2);
  }
  return `${endSessionEndpoint}?${urlSearchParameters.toString()}`;
};

// node_modules/@logto/js/lib/core/user-info.js
var fetchUserInfo = async (userInfoEndpoint, accessToken2, requester) => requester(userInfoEndpoint, {
  headers: { Authorization: `Bearer ${accessToken2}` }
});

// node_modules/@silverhand/essentials/lib/utilities/array.js
var deduplicate = (array) => [...new Set(array)];

// node_modules/@silverhand/essentials/lib/utilities/assertions.js
var notFalsy = (value) => Boolean(value);

// node_modules/@silverhand/essentials/lib/utilities/conditional.js
var conditional = (exp) => notFalsy(exp) ? exp : void 0;
var conditionalString = (exp) => notFalsy(exp) ? String(exp) : "";

// node_modules/@silverhand/essentials/lib/utilities/function.js
var isPromise = (value) => value !== null && (typeof value === "object" || typeof value === "function") && "then" in value && typeof value.then === "function";
var trySafe = (exec, onError) => {
  try {
    const unwrapped = typeof exec === "function" ? exec() : exec;
    return isPromise(unwrapped) ? (
      // eslint-disable-next-line promise/prefer-await-to-then
      unwrapped.catch((error) => {
        onError?.(error);
      })
    ) : unwrapped;
  } catch (error) {
    onError?.(error);
  }
};

// node_modules/@silverhand/essentials/lib/utilities/string.js
var replaceNonUrlSafeCharacters = (base64String) => base64String.replaceAll("+", "-").replaceAll("/", "_").replaceAll(/=+$/g, "");
var restoreNonUrlSafeCharacters = (base64String) => base64String.replaceAll("-", "+").replaceAll("_", "/");
var urlSafeBase64 = {
  isSafe: (input) => /^[\w-]*$/.test(input),
  encode: (rawString) => {
    const encodedString = btoa(unescape(encodeURIComponent(rawString)));
    return replaceNonUrlSafeCharacters(encodedString);
  },
  decode: (encodedString) => {
    const nonUrlSafeEncodedString = restoreNonUrlSafeCharacters(encodedString);
    return decodeURIComponent(escape(atob(nonUrlSafeEncodedString)));
  },
  replaceNonUrlSafeCharacters,
  restoreNonUrlSafeCharacters
};

// node_modules/@silverhand/essentials/lib/utilities/url.js
var joinPath = (...segments) => {
  const result = [];
  for (const segment of segments.join("/").split("/")) {
    if (!segment || segment === ".") {
      continue;
    }
    if ([...segment].every((char) => char === ".")) {
      result.pop();
      continue;
    }
    result.push(segment);
  }
  return "/" + result.join("/");
};
var appendPath = (baseUrl, ...pathnames) => new URL(joinPath(baseUrl.pathname, ...pathnames), baseUrl);

// node_modules/@logto/js/lib/utils/arbitrary-object.js
var isArbitraryObject = (data) => typeof data === "object" && data !== null;

// node_modules/@logto/js/lib/utils/errors.js
var logtoErrorCodes = Object.freeze({
  "id_token.invalid_iat": "Invalid issued at time in the ID token",
  "id_token.invalid_token": "Invalid ID token",
  "callback_uri_verification.redirect_uri_mismatched": "The callback URI mismatches the redirect URI.",
  "callback_uri_verification.error_found": "Error found in the callback URI",
  "callback_uri_verification.missing_state": "Missing state in the callback URI",
  "callback_uri_verification.state_mismatched": "State mismatched in the callback URI",
  "callback_uri_verification.missing_code": "Missing code in the callback URI",
  crypto_subtle_unavailable: "Crypto.subtle is unavailable in insecure contexts (non-HTTPS).",
  unexpected_response_error: "Unexpected response error from the server."
});
var LogtoError = class extends Error {
  constructor(code, data) {
    super(logtoErrorCodes[code]);
    this.code = code;
    this.data = data;
    this.name = "LogtoError";
  }
};
var isLogtoRequestErrorJson = (data) => {
  if (!isArbitraryObject(data)) {
    return false;
  }
  return typeof data.code === "string" && typeof data.message === "string";
};
var LogtoRequestError = class extends Error {
  constructor(code, message2, cause) {
    super(message2);
    this.code = code;
    this.cause = cause;
    this.name = "LogtoRequestError";
  }
};
var OidcError = class {
  constructor(error, errorDescription) {
    this.error = error;
    this.errorDescription = errorDescription;
    this.name = "OidcError";
  }
};

// node_modules/@logto/js/lib/utils/callback-uri.js
var parseUriParameters = (uri) => {
  const [, queryString = ""] = uri.split("?");
  return new URLSearchParams(queryString);
};
var verifyAndParseCodeFromCallbackUri = (callbackUri, redirectUri2, state) => {
  if (!callbackUri.startsWith(redirectUri2)) {
    throw new LogtoError("callback_uri_verification.redirect_uri_mismatched");
  }
  const uriParameters = parseUriParameters(callbackUri);
  const error = conditional(uriParameters.get(QueryKey.Error));
  const errorDescription = conditional(uriParameters.get(QueryKey.ErrorDescription));
  if (error) {
    throw new LogtoError("callback_uri_verification.error_found", new OidcError(error, errorDescription));
  }
  const stateFromCallbackUri = uriParameters.get(QueryKey.State);
  if (!stateFromCallbackUri) {
    throw new LogtoError("callback_uri_verification.missing_state");
  }
  if (stateFromCallbackUri !== state) {
    throw new LogtoError("callback_uri_verification.state_mismatched");
  }
  const code = uriParameters.get(QueryKey.Code);
  if (!code) {
    throw new LogtoError("callback_uri_verification.missing_code");
  }
  return code;
};

// node_modules/@logto/js/lib/utils/id-token.js
function assertIdTokenClaims(data) {
  if (!isArbitraryObject(data)) {
    throw new TypeError("IdToken is expected to be an object");
  }
  for (const key of ["iss", "sub", "aud"]) {
    if (typeof data[key] !== "string") {
      throw new TypeError(`At path: IdToken.${key}: expected a string`);
    }
  }
  for (const key of ["exp", "iat"]) {
    if (typeof data[key] !== "number") {
      throw new TypeError(`At path: IdToken.${key}: expected a number`);
    }
  }
  for (const key of ["at_hash", "name", "username", "picture", "email", "phone_number"]) {
    if (data[key] === void 0) {
      continue;
    }
    if (typeof data[key] !== "string" && data[key] !== null) {
      throw new TypeError(`At path: IdToken.${key}: expected null or a string`);
    }
  }
  for (const key of ["email_verified", "phone_number_verified"]) {
    if (data[key] === void 0) {
      continue;
    }
    if (typeof data[key] !== "boolean") {
      throw new TypeError(`At path: IdToken.${key}: expected a boolean`);
    }
  }
}
var decodeIdToken = (token) => {
  const { 1: encodedPayload } = token.split(".");
  if (!encodedPayload) {
    throw new LogtoError("id_token.invalid_token");
  }
  const json = urlSafeBase64.decode(encodedPayload);
  const idTokenClaims2 = JSON.parse(json);
  assertIdTokenClaims(idTokenClaims2);
  return idTokenClaims2;
};

// node_modules/@logto/js/lib/utils/access-token.js
function assertAccessTokenClaims(data) {
  if (!isArbitraryObject(data)) {
    throw new TypeError("AccessToken is expected to be an object");
  }
  for (const key of ["jti", "iss", "sub", "aud", "client_id", "scope"]) {
    if (data[key] === void 0) {
      continue;
    }
    if (typeof data[key] !== "string" && data[key] !== null) {
      throw new TypeError(`At path: AccessToken.${key}: expected null or a string`);
    }
  }
  for (const key of ["exp", "iat"]) {
    if (data[key] === void 0) {
      continue;
    }
    if (typeof data[key] !== "number" && data[key] !== null) {
      throw new TypeError(`At path: AccessToken.${key}: expected null or a number`);
    }
  }
}
var decodeAccessToken = (accessToken2) => {
  const { 1: encodedPayload } = accessToken2.split(".");
  if (!encodedPayload) {
    return {};
  }
  const json = urlSafeBase64.decode(encodedPayload);
  const accessTokenClaims = JSON.parse(json);
  assertAccessTokenClaims(accessTokenClaims);
  return accessTokenClaims;
};

// node_modules/jose/dist/browser/runtime/webcrypto.js
var webcrypto_default = crypto;
var isCryptoKey = (key) => key instanceof CryptoKey;

// node_modules/jose/dist/browser/lib/buffer_utils.js
var encoder = new TextEncoder();
var decoder = new TextDecoder();
var MAX_INT32 = 2 ** 32;
function concat(...buffers) {
  const size = buffers.reduce((acc, { length }) => acc + length, 0);
  const buf = new Uint8Array(size);
  let i = 0;
  for (const buffer of buffers) {
    buf.set(buffer, i);
    i += buffer.length;
  }
  return buf;
}

// node_modules/jose/dist/browser/runtime/base64url.js
var decodeBase64 = (encoded) => {
  const binary = atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
};
var decode = (input) => {
  let encoded = input;
  if (encoded instanceof Uint8Array) {
    encoded = decoder.decode(encoded);
  }
  encoded = encoded.replace(/-/g, "+").replace(/_/g, "/").replace(/\s/g, "");
  try {
    return decodeBase64(encoded);
  } catch {
    throw new TypeError("The input to be decoded is not correctly encoded.");
  }
};

// node_modules/jose/dist/browser/util/errors.js
var JOSEError = class extends Error {
  constructor(message2, options) {
    super(message2, options);
    this.code = "ERR_JOSE_GENERIC";
    this.name = this.constructor.name;
    Error.captureStackTrace?.(this, this.constructor);
  }
};
JOSEError.code = "ERR_JOSE_GENERIC";
var JWTClaimValidationFailed = class extends JOSEError {
  constructor(message2, payload, claim = "unspecified", reason = "unspecified") {
    super(message2, { cause: { claim, reason, payload } });
    this.code = "ERR_JWT_CLAIM_VALIDATION_FAILED";
    this.claim = claim;
    this.reason = reason;
    this.payload = payload;
  }
};
JWTClaimValidationFailed.code = "ERR_JWT_CLAIM_VALIDATION_FAILED";
var JWTExpired = class extends JOSEError {
  constructor(message2, payload, claim = "unspecified", reason = "unspecified") {
    super(message2, { cause: { claim, reason, payload } });
    this.code = "ERR_JWT_EXPIRED";
    this.claim = claim;
    this.reason = reason;
    this.payload = payload;
  }
};
JWTExpired.code = "ERR_JWT_EXPIRED";
var JOSEAlgNotAllowed = class extends JOSEError {
  constructor() {
    super(...arguments);
    this.code = "ERR_JOSE_ALG_NOT_ALLOWED";
  }
};
JOSEAlgNotAllowed.code = "ERR_JOSE_ALG_NOT_ALLOWED";
var JOSENotSupported = class extends JOSEError {
  constructor() {
    super(...arguments);
    this.code = "ERR_JOSE_NOT_SUPPORTED";
  }
};
JOSENotSupported.code = "ERR_JOSE_NOT_SUPPORTED";
var JWEDecryptionFailed = class extends JOSEError {
  constructor(message2 = "decryption operation failed", options) {
    super(message2, options);
    this.code = "ERR_JWE_DECRYPTION_FAILED";
  }
};
JWEDecryptionFailed.code = "ERR_JWE_DECRYPTION_FAILED";
var JWEInvalid = class extends JOSEError {
  constructor() {
    super(...arguments);
    this.code = "ERR_JWE_INVALID";
  }
};
JWEInvalid.code = "ERR_JWE_INVALID";
var JWSInvalid = class extends JOSEError {
  constructor() {
    super(...arguments);
    this.code = "ERR_JWS_INVALID";
  }
};
JWSInvalid.code = "ERR_JWS_INVALID";
var JWTInvalid = class extends JOSEError {
  constructor() {
    super(...arguments);
    this.code = "ERR_JWT_INVALID";
  }
};
JWTInvalid.code = "ERR_JWT_INVALID";
var JWKInvalid = class extends JOSEError {
  constructor() {
    super(...arguments);
    this.code = "ERR_JWK_INVALID";
  }
};
JWKInvalid.code = "ERR_JWK_INVALID";
var JWKSInvalid = class extends JOSEError {
  constructor() {
    super(...arguments);
    this.code = "ERR_JWKS_INVALID";
  }
};
JWKSInvalid.code = "ERR_JWKS_INVALID";
var JWKSNoMatchingKey = class extends JOSEError {
  constructor(message2 = "no applicable key found in the JSON Web Key Set", options) {
    super(message2, options);
    this.code = "ERR_JWKS_NO_MATCHING_KEY";
  }
};
JWKSNoMatchingKey.code = "ERR_JWKS_NO_MATCHING_KEY";
var JWKSMultipleMatchingKeys = class extends JOSEError {
  constructor(message2 = "multiple matching keys found in the JSON Web Key Set", options) {
    super(message2, options);
    this.code = "ERR_JWKS_MULTIPLE_MATCHING_KEYS";
  }
};
JWKSMultipleMatchingKeys.code = "ERR_JWKS_MULTIPLE_MATCHING_KEYS";
var JWKSTimeout = class extends JOSEError {
  constructor(message2 = "request timed out", options) {
    super(message2, options);
    this.code = "ERR_JWKS_TIMEOUT";
  }
};
JWKSTimeout.code = "ERR_JWKS_TIMEOUT";
var JWSSignatureVerificationFailed = class extends JOSEError {
  constructor(message2 = "signature verification failed", options) {
    super(message2, options);
    this.code = "ERR_JWS_SIGNATURE_VERIFICATION_FAILED";
  }
};
JWSSignatureVerificationFailed.code = "ERR_JWS_SIGNATURE_VERIFICATION_FAILED";

// node_modules/jose/dist/browser/lib/crypto_key.js
function unusable(name, prop = "algorithm.name") {
  return new TypeError(`CryptoKey does not support this operation, its ${prop} must be ${name}`);
}
function isAlgorithm(algorithm, name) {
  return algorithm.name === name;
}
function getHashLength(hash) {
  return parseInt(hash.name.slice(4), 10);
}
function getNamedCurve(alg) {
  switch (alg) {
    case "ES256":
      return "P-256";
    case "ES384":
      return "P-384";
    case "ES512":
      return "P-521";
    default:
      throw new Error("unreachable");
  }
}
function checkUsage(key, usages) {
  if (usages.length && !usages.some((expected) => key.usages.includes(expected))) {
    let msg = "CryptoKey does not support this operation, its usages must include ";
    if (usages.length > 2) {
      const last = usages.pop();
      msg += `one of ${usages.join(", ")}, or ${last}.`;
    } else if (usages.length === 2) {
      msg += `one of ${usages[0]} or ${usages[1]}.`;
    } else {
      msg += `${usages[0]}.`;
    }
    throw new TypeError(msg);
  }
}
function checkSigCryptoKey(key, alg, ...usages) {
  switch (alg) {
    case "HS256":
    case "HS384":
    case "HS512": {
      if (!isAlgorithm(key.algorithm, "HMAC"))
        throw unusable("HMAC");
      const expected = parseInt(alg.slice(2), 10);
      const actual = getHashLength(key.algorithm.hash);
      if (actual !== expected)
        throw unusable(`SHA-${expected}`, "algorithm.hash");
      break;
    }
    case "RS256":
    case "RS384":
    case "RS512": {
      if (!isAlgorithm(key.algorithm, "RSASSA-PKCS1-v1_5"))
        throw unusable("RSASSA-PKCS1-v1_5");
      const expected = parseInt(alg.slice(2), 10);
      const actual = getHashLength(key.algorithm.hash);
      if (actual !== expected)
        throw unusable(`SHA-${expected}`, "algorithm.hash");
      break;
    }
    case "PS256":
    case "PS384":
    case "PS512": {
      if (!isAlgorithm(key.algorithm, "RSA-PSS"))
        throw unusable("RSA-PSS");
      const expected = parseInt(alg.slice(2), 10);
      const actual = getHashLength(key.algorithm.hash);
      if (actual !== expected)
        throw unusable(`SHA-${expected}`, "algorithm.hash");
      break;
    }
    case "EdDSA": {
      if (key.algorithm.name !== "Ed25519" && key.algorithm.name !== "Ed448") {
        throw unusable("Ed25519 or Ed448");
      }
      break;
    }
    case "Ed25519": {
      if (!isAlgorithm(key.algorithm, "Ed25519"))
        throw unusable("Ed25519");
      break;
    }
    case "ES256":
    case "ES384":
    case "ES512": {
      if (!isAlgorithm(key.algorithm, "ECDSA"))
        throw unusable("ECDSA");
      const expected = getNamedCurve(alg);
      const actual = key.algorithm.namedCurve;
      if (actual !== expected)
        throw unusable(expected, "algorithm.namedCurve");
      break;
    }
    default:
      throw new TypeError("CryptoKey does not support this operation");
  }
  checkUsage(key, usages);
}

// node_modules/jose/dist/browser/lib/invalid_key_input.js
function message(msg, actual, ...types2) {
  types2 = types2.filter(Boolean);
  if (types2.length > 2) {
    const last = types2.pop();
    msg += `one of type ${types2.join(", ")}, or ${last}.`;
  } else if (types2.length === 2) {
    msg += `one of type ${types2[0]} or ${types2[1]}.`;
  } else {
    msg += `of type ${types2[0]}.`;
  }
  if (actual == null) {
    msg += ` Received ${actual}`;
  } else if (typeof actual === "function" && actual.name) {
    msg += ` Received function ${actual.name}`;
  } else if (typeof actual === "object" && actual != null) {
    if (actual.constructor?.name) {
      msg += ` Received an instance of ${actual.constructor.name}`;
    }
  }
  return msg;
}
var invalid_key_input_default = (actual, ...types2) => {
  return message("Key must be ", actual, ...types2);
};
function withAlg(alg, actual, ...types2) {
  return message(`Key for the ${alg} algorithm must be `, actual, ...types2);
}

// node_modules/jose/dist/browser/runtime/is_key_like.js
var is_key_like_default = (key) => {
  if (isCryptoKey(key)) {
    return true;
  }
  return key?.[Symbol.toStringTag] === "KeyObject";
};
var types = ["CryptoKey"];

// node_modules/jose/dist/browser/lib/is_disjoint.js
var isDisjoint = (...headers) => {
  const sources = headers.filter(Boolean);
  if (sources.length === 0 || sources.length === 1) {
    return true;
  }
  let acc;
  for (const header of sources) {
    const parameters = Object.keys(header);
    if (!acc || acc.size === 0) {
      acc = new Set(parameters);
      continue;
    }
    for (const parameter of parameters) {
      if (acc.has(parameter)) {
        return false;
      }
      acc.add(parameter);
    }
  }
  return true;
};
var is_disjoint_default = isDisjoint;

// node_modules/jose/dist/browser/lib/is_object.js
function isObjectLike(value) {
  return typeof value === "object" && value !== null;
}
function isObject4(input) {
  if (!isObjectLike(input) || Object.prototype.toString.call(input) !== "[object Object]") {
    return false;
  }
  if (Object.getPrototypeOf(input) === null) {
    return true;
  }
  let proto = input;
  while (Object.getPrototypeOf(proto) !== null) {
    proto = Object.getPrototypeOf(proto);
  }
  return Object.getPrototypeOf(input) === proto;
}

// node_modules/jose/dist/browser/runtime/check_key_length.js
var check_key_length_default = (alg, key) => {
  if (alg.startsWith("RS") || alg.startsWith("PS")) {
    const { modulusLength } = key.algorithm;
    if (typeof modulusLength !== "number" || modulusLength < 2048) {
      throw new TypeError(`${alg} requires key modulusLength to be 2048 bits or larger`);
    }
  }
};

// node_modules/jose/dist/browser/lib/is_jwk.js
function isJWK(key) {
  return isObject4(key) && typeof key.kty === "string";
}
function isPrivateJWK(key) {
  return key.kty !== "oct" && typeof key.d === "string";
}
function isPublicJWK(key) {
  return key.kty !== "oct" && typeof key.d === "undefined";
}
function isSecretJWK(key) {
  return isJWK(key) && key.kty === "oct" && typeof key.k === "string";
}

// node_modules/jose/dist/browser/runtime/jwk_to_key.js
function subtleMapping(jwk) {
  let algorithm;
  let keyUsages;
  switch (jwk.kty) {
    case "RSA": {
      switch (jwk.alg) {
        case "PS256":
        case "PS384":
        case "PS512":
          algorithm = { name: "RSA-PSS", hash: `SHA-${jwk.alg.slice(-3)}` };
          keyUsages = jwk.d ? ["sign"] : ["verify"];
          break;
        case "RS256":
        case "RS384":
        case "RS512":
          algorithm = { name: "RSASSA-PKCS1-v1_5", hash: `SHA-${jwk.alg.slice(-3)}` };
          keyUsages = jwk.d ? ["sign"] : ["verify"];
          break;
        case "RSA-OAEP":
        case "RSA-OAEP-256":
        case "RSA-OAEP-384":
        case "RSA-OAEP-512":
          algorithm = {
            name: "RSA-OAEP",
            hash: `SHA-${parseInt(jwk.alg.slice(-3), 10) || 1}`
          };
          keyUsages = jwk.d ? ["decrypt", "unwrapKey"] : ["encrypt", "wrapKey"];
          break;
        default:
          throw new JOSENotSupported('Invalid or unsupported JWK "alg" (Algorithm) Parameter value');
      }
      break;
    }
    case "EC": {
      switch (jwk.alg) {
        case "ES256":
          algorithm = { name: "ECDSA", namedCurve: "P-256" };
          keyUsages = jwk.d ? ["sign"] : ["verify"];
          break;
        case "ES384":
          algorithm = { name: "ECDSA", namedCurve: "P-384" };
          keyUsages = jwk.d ? ["sign"] : ["verify"];
          break;
        case "ES512":
          algorithm = { name: "ECDSA", namedCurve: "P-521" };
          keyUsages = jwk.d ? ["sign"] : ["verify"];
          break;
        case "ECDH-ES":
        case "ECDH-ES+A128KW":
        case "ECDH-ES+A192KW":
        case "ECDH-ES+A256KW":
          algorithm = { name: "ECDH", namedCurve: jwk.crv };
          keyUsages = jwk.d ? ["deriveBits"] : [];
          break;
        default:
          throw new JOSENotSupported('Invalid or unsupported JWK "alg" (Algorithm) Parameter value');
      }
      break;
    }
    case "OKP": {
      switch (jwk.alg) {
        case "Ed25519":
          algorithm = { name: "Ed25519" };
          keyUsages = jwk.d ? ["sign"] : ["verify"];
          break;
        case "EdDSA":
          algorithm = { name: jwk.crv };
          keyUsages = jwk.d ? ["sign"] : ["verify"];
          break;
        case "ECDH-ES":
        case "ECDH-ES+A128KW":
        case "ECDH-ES+A192KW":
        case "ECDH-ES+A256KW":
          algorithm = { name: jwk.crv };
          keyUsages = jwk.d ? ["deriveBits"] : [];
          break;
        default:
          throw new JOSENotSupported('Invalid or unsupported JWK "alg" (Algorithm) Parameter value');
      }
      break;
    }
    default:
      throw new JOSENotSupported('Invalid or unsupported JWK "kty" (Key Type) Parameter value');
  }
  return { algorithm, keyUsages };
}
var parse = async (jwk) => {
  if (!jwk.alg) {
    throw new TypeError('"alg" argument is required when "jwk.alg" is not present');
  }
  const { algorithm, keyUsages } = subtleMapping(jwk);
  const rest = [
    algorithm,
    jwk.ext ?? false,
    jwk.key_ops ?? keyUsages
  ];
  const keyData = { ...jwk };
  delete keyData.alg;
  delete keyData.use;
  return webcrypto_default.subtle.importKey("jwk", keyData, ...rest);
};
var jwk_to_key_default = parse;

// node_modules/jose/dist/browser/runtime/normalize_key.js
var exportKeyValue = (k) => decode(k);
var privCache;
var pubCache;
var isKeyObject = (key) => {
  return key?.[Symbol.toStringTag] === "KeyObject";
};
var importAndCache = async (cache2, key, jwk, alg, freeze = false) => {
  let cached = cache2.get(key);
  if (cached?.[alg]) {
    return cached[alg];
  }
  const cryptoKey = await jwk_to_key_default({ ...jwk, alg });
  if (freeze)
    Object.freeze(key);
  if (!cached) {
    cache2.set(key, { [alg]: cryptoKey });
  } else {
    cached[alg] = cryptoKey;
  }
  return cryptoKey;
};
var normalizePublicKey = (key, alg) => {
  if (isKeyObject(key)) {
    let jwk = key.export({ format: "jwk" });
    delete jwk.d;
    delete jwk.dp;
    delete jwk.dq;
    delete jwk.p;
    delete jwk.q;
    delete jwk.qi;
    if (jwk.k) {
      return exportKeyValue(jwk.k);
    }
    pubCache || (pubCache = /* @__PURE__ */ new WeakMap());
    return importAndCache(pubCache, key, jwk, alg);
  }
  if (isJWK(key)) {
    if (key.k)
      return decode(key.k);
    pubCache || (pubCache = /* @__PURE__ */ new WeakMap());
    const cryptoKey = importAndCache(pubCache, key, key, alg, true);
    return cryptoKey;
  }
  return key;
};
var normalizePrivateKey = (key, alg) => {
  if (isKeyObject(key)) {
    let jwk = key.export({ format: "jwk" });
    if (jwk.k) {
      return exportKeyValue(jwk.k);
    }
    privCache || (privCache = /* @__PURE__ */ new WeakMap());
    return importAndCache(privCache, key, jwk, alg);
  }
  if (isJWK(key)) {
    if (key.k)
      return decode(key.k);
    privCache || (privCache = /* @__PURE__ */ new WeakMap());
    const cryptoKey = importAndCache(privCache, key, key, alg, true);
    return cryptoKey;
  }
  return key;
};
var normalize_key_default = { normalizePublicKey, normalizePrivateKey };

// node_modules/jose/dist/browser/key/import.js
async function importJWK(jwk, alg) {
  if (!isObject4(jwk)) {
    throw new TypeError("JWK must be an object");
  }
  alg || (alg = jwk.alg);
  switch (jwk.kty) {
    case "oct":
      if (typeof jwk.k !== "string" || !jwk.k) {
        throw new TypeError('missing "k" (Key Value) Parameter value');
      }
      return decode(jwk.k);
    case "RSA":
      if ("oth" in jwk && jwk.oth !== void 0) {
        throw new JOSENotSupported('RSA JWK "oth" (Other Primes Info) Parameter value is not supported');
      }
    case "EC":
    case "OKP":
      return jwk_to_key_default({ ...jwk, alg });
    default:
      throw new JOSENotSupported('Unsupported "kty" (Key Type) Parameter value');
  }
}

// node_modules/jose/dist/browser/lib/check_key_type.js
var tag = (key) => key?.[Symbol.toStringTag];
var jwkMatchesOp = (alg, key, usage) => {
  if (key.use !== void 0 && key.use !== "sig") {
    throw new TypeError("Invalid key for this operation, when present its use must be sig");
  }
  if (key.key_ops !== void 0 && key.key_ops.includes?.(usage) !== true) {
    throw new TypeError(`Invalid key for this operation, when present its key_ops must include ${usage}`);
  }
  if (key.alg !== void 0 && key.alg !== alg) {
    throw new TypeError(`Invalid key for this operation, when present its alg must be ${alg}`);
  }
  return true;
};
var symmetricTypeCheck = (alg, key, usage, allowJwk) => {
  if (key instanceof Uint8Array)
    return;
  if (allowJwk && isJWK(key)) {
    if (isSecretJWK(key) && jwkMatchesOp(alg, key, usage))
      return;
    throw new TypeError(`JSON Web Key for symmetric algorithms must have JWK "kty" (Key Type) equal to "oct" and the JWK "k" (Key Value) present`);
  }
  if (!is_key_like_default(key)) {
    throw new TypeError(withAlg(alg, key, ...types, "Uint8Array", allowJwk ? "JSON Web Key" : null));
  }
  if (key.type !== "secret") {
    throw new TypeError(`${tag(key)} instances for symmetric algorithms must be of type "secret"`);
  }
};
var asymmetricTypeCheck = (alg, key, usage, allowJwk) => {
  if (allowJwk && isJWK(key)) {
    switch (usage) {
      case "sign":
        if (isPrivateJWK(key) && jwkMatchesOp(alg, key, usage))
          return;
        throw new TypeError(`JSON Web Key for this operation be a private JWK`);
      case "verify":
        if (isPublicJWK(key) && jwkMatchesOp(alg, key, usage))
          return;
        throw new TypeError(`JSON Web Key for this operation be a public JWK`);
    }
  }
  if (!is_key_like_default(key)) {
    throw new TypeError(withAlg(alg, key, ...types, allowJwk ? "JSON Web Key" : null));
  }
  if (key.type === "secret") {
    throw new TypeError(`${tag(key)} instances for asymmetric algorithms must not be of type "secret"`);
  }
  if (usage === "sign" && key.type === "public") {
    throw new TypeError(`${tag(key)} instances for asymmetric algorithm signing must be of type "private"`);
  }
  if (usage === "decrypt" && key.type === "public") {
    throw new TypeError(`${tag(key)} instances for asymmetric algorithm decryption must be of type "private"`);
  }
  if (key.algorithm && usage === "verify" && key.type === "private") {
    throw new TypeError(`${tag(key)} instances for asymmetric algorithm verifying must be of type "public"`);
  }
  if (key.algorithm && usage === "encrypt" && key.type === "private") {
    throw new TypeError(`${tag(key)} instances for asymmetric algorithm encryption must be of type "public"`);
  }
};
function checkKeyType(allowJwk, alg, key, usage) {
  const symmetric = alg.startsWith("HS") || alg === "dir" || alg.startsWith("PBES2") || /^A\d{3}(?:GCM)?KW$/.test(alg);
  if (symmetric) {
    symmetricTypeCheck(alg, key, usage, allowJwk);
  } else {
    asymmetricTypeCheck(alg, key, usage, allowJwk);
  }
}
var check_key_type_default = checkKeyType.bind(void 0, false);
var checkKeyTypeWithJwk = checkKeyType.bind(void 0, true);

// node_modules/jose/dist/browser/lib/validate_crit.js
function validateCrit(Err, recognizedDefault, recognizedOption, protectedHeader, joseHeader) {
  if (joseHeader.crit !== void 0 && protectedHeader?.crit === void 0) {
    throw new Err('"crit" (Critical) Header Parameter MUST be integrity protected');
  }
  if (!protectedHeader || protectedHeader.crit === void 0) {
    return /* @__PURE__ */ new Set();
  }
  if (!Array.isArray(protectedHeader.crit) || protectedHeader.crit.length === 0 || protectedHeader.crit.some((input) => typeof input !== "string" || input.length === 0)) {
    throw new Err('"crit" (Critical) Header Parameter MUST be an array of non-empty strings when present');
  }
  let recognized;
  if (recognizedOption !== void 0) {
    recognized = new Map([...Object.entries(recognizedOption), ...recognizedDefault.entries()]);
  } else {
    recognized = recognizedDefault;
  }
  for (const parameter of protectedHeader.crit) {
    if (!recognized.has(parameter)) {
      throw new JOSENotSupported(`Extension Header Parameter "${parameter}" is not recognized`);
    }
    if (joseHeader[parameter] === void 0) {
      throw new Err(`Extension Header Parameter "${parameter}" is missing`);
    }
    if (recognized.get(parameter) && protectedHeader[parameter] === void 0) {
      throw new Err(`Extension Header Parameter "${parameter}" MUST be integrity protected`);
    }
  }
  return new Set(protectedHeader.crit);
}
var validate_crit_default = validateCrit;

// node_modules/jose/dist/browser/lib/validate_algorithms.js
var validateAlgorithms = (option, algorithms) => {
  if (algorithms !== void 0 && (!Array.isArray(algorithms) || algorithms.some((s) => typeof s !== "string"))) {
    throw new TypeError(`"${option}" option must be an array of strings`);
  }
  if (!algorithms) {
    return void 0;
  }
  return new Set(algorithms);
};
var validate_algorithms_default = validateAlgorithms;

// node_modules/jose/dist/browser/runtime/subtle_dsa.js
function subtleDsa(alg, algorithm) {
  const hash = `SHA-${alg.slice(-3)}`;
  switch (alg) {
    case "HS256":
    case "HS384":
    case "HS512":
      return { hash, name: "HMAC" };
    case "PS256":
    case "PS384":
    case "PS512":
      return { hash, name: "RSA-PSS", saltLength: alg.slice(-3) >> 3 };
    case "RS256":
    case "RS384":
    case "RS512":
      return { hash, name: "RSASSA-PKCS1-v1_5" };
    case "ES256":
    case "ES384":
    case "ES512":
      return { hash, name: "ECDSA", namedCurve: algorithm.namedCurve };
    case "Ed25519":
      return { name: "Ed25519" };
    case "EdDSA":
      return { name: algorithm.name };
    default:
      throw new JOSENotSupported(`alg ${alg} is not supported either by JOSE or your javascript runtime`);
  }
}

// node_modules/jose/dist/browser/runtime/get_sign_verify_key.js
async function getCryptoKey(alg, key, usage) {
  if (usage === "sign") {
    key = await normalize_key_default.normalizePrivateKey(key, alg);
  }
  if (usage === "verify") {
    key = await normalize_key_default.normalizePublicKey(key, alg);
  }
  if (isCryptoKey(key)) {
    checkSigCryptoKey(key, alg, usage);
    return key;
  }
  if (key instanceof Uint8Array) {
    if (!alg.startsWith("HS")) {
      throw new TypeError(invalid_key_input_default(key, ...types));
    }
    return webcrypto_default.subtle.importKey("raw", key, { hash: `SHA-${alg.slice(-3)}`, name: "HMAC" }, false, [usage]);
  }
  throw new TypeError(invalid_key_input_default(key, ...types, "Uint8Array", "JSON Web Key"));
}

// node_modules/jose/dist/browser/runtime/verify.js
var verify = async (alg, key, signature, data) => {
  const cryptoKey = await getCryptoKey(alg, key, "verify");
  check_key_length_default(alg, cryptoKey);
  const algorithm = subtleDsa(alg, cryptoKey.algorithm);
  try {
    return await webcrypto_default.subtle.verify(algorithm, cryptoKey, signature, data);
  } catch {
    return false;
  }
};
var verify_default = verify;

// node_modules/jose/dist/browser/jws/flattened/verify.js
async function flattenedVerify(jws, key, options) {
  if (!isObject4(jws)) {
    throw new JWSInvalid("Flattened JWS must be an object");
  }
  if (jws.protected === void 0 && jws.header === void 0) {
    throw new JWSInvalid('Flattened JWS must have either of the "protected" or "header" members');
  }
  if (jws.protected !== void 0 && typeof jws.protected !== "string") {
    throw new JWSInvalid("JWS Protected Header incorrect type");
  }
  if (jws.payload === void 0) {
    throw new JWSInvalid("JWS Payload missing");
  }
  if (typeof jws.signature !== "string") {
    throw new JWSInvalid("JWS Signature missing or incorrect type");
  }
  if (jws.header !== void 0 && !isObject4(jws.header)) {
    throw new JWSInvalid("JWS Unprotected Header incorrect type");
  }
  let parsedProt = {};
  if (jws.protected) {
    try {
      const protectedHeader = decode(jws.protected);
      parsedProt = JSON.parse(decoder.decode(protectedHeader));
    } catch {
      throw new JWSInvalid("JWS Protected Header is invalid");
    }
  }
  if (!is_disjoint_default(parsedProt, jws.header)) {
    throw new JWSInvalid("JWS Protected and JWS Unprotected Header Parameter names must be disjoint");
  }
  const joseHeader = {
    ...parsedProt,
    ...jws.header
  };
  const extensions = validate_crit_default(JWSInvalid, /* @__PURE__ */ new Map([["b64", true]]), options?.crit, parsedProt, joseHeader);
  let b64 = true;
  if (extensions.has("b64")) {
    b64 = parsedProt.b64;
    if (typeof b64 !== "boolean") {
      throw new JWSInvalid('The "b64" (base64url-encode payload) Header Parameter must be a boolean');
    }
  }
  const { alg } = joseHeader;
  if (typeof alg !== "string" || !alg) {
    throw new JWSInvalid('JWS "alg" (Algorithm) Header Parameter missing or invalid');
  }
  const algorithms = options && validate_algorithms_default("algorithms", options.algorithms);
  if (algorithms && !algorithms.has(alg)) {
    throw new JOSEAlgNotAllowed('"alg" (Algorithm) Header Parameter value not allowed');
  }
  if (b64) {
    if (typeof jws.payload !== "string") {
      throw new JWSInvalid("JWS Payload must be a string");
    }
  } else if (typeof jws.payload !== "string" && !(jws.payload instanceof Uint8Array)) {
    throw new JWSInvalid("JWS Payload must be a string or an Uint8Array instance");
  }
  let resolvedKey = false;
  if (typeof key === "function") {
    key = await key(parsedProt, jws);
    resolvedKey = true;
    checkKeyTypeWithJwk(alg, key, "verify");
    if (isJWK(key)) {
      key = await importJWK(key, alg);
    }
  } else {
    checkKeyTypeWithJwk(alg, key, "verify");
  }
  const data = concat(encoder.encode(jws.protected ?? ""), encoder.encode("."), typeof jws.payload === "string" ? encoder.encode(jws.payload) : jws.payload);
  let signature;
  try {
    signature = decode(jws.signature);
  } catch {
    throw new JWSInvalid("Failed to base64url decode the signature");
  }
  const verified = await verify_default(alg, key, signature, data);
  if (!verified) {
    throw new JWSSignatureVerificationFailed();
  }
  let payload;
  if (b64) {
    try {
      payload = decode(jws.payload);
    } catch {
      throw new JWSInvalid("Failed to base64url decode the payload");
    }
  } else if (typeof jws.payload === "string") {
    payload = encoder.encode(jws.payload);
  } else {
    payload = jws.payload;
  }
  const result = { payload };
  if (jws.protected !== void 0) {
    result.protectedHeader = parsedProt;
  }
  if (jws.header !== void 0) {
    result.unprotectedHeader = jws.header;
  }
  if (resolvedKey) {
    return { ...result, key };
  }
  return result;
}

// node_modules/jose/dist/browser/jws/compact/verify.js
async function compactVerify(jws, key, options) {
  if (jws instanceof Uint8Array) {
    jws = decoder.decode(jws);
  }
  if (typeof jws !== "string") {
    throw new JWSInvalid("Compact JWS must be a string or Uint8Array");
  }
  const { 0: protectedHeader, 1: payload, 2: signature, length } = jws.split(".");
  if (length !== 3) {
    throw new JWSInvalid("Invalid Compact JWS");
  }
  const verified = await flattenedVerify({ payload, protected: protectedHeader, signature }, key, options);
  const result = { payload: verified.payload, protectedHeader: verified.protectedHeader };
  if (typeof key === "function") {
    return { ...result, key: verified.key };
  }
  return result;
}

// node_modules/jose/dist/browser/lib/epoch.js
var epoch_default = (date) => Math.floor(date.getTime() / 1e3);

// node_modules/jose/dist/browser/lib/secs.js
var minute = 60;
var hour = minute * 60;
var day = hour * 24;
var week = day * 7;
var year = day * 365.25;
var REGEX = /^(\+|\-)? ?(\d+|\d+\.\d+) ?(seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h|days?|d|weeks?|w|years?|yrs?|y)(?: (ago|from now))?$/i;
var secs_default = (str) => {
  const matched = REGEX.exec(str);
  if (!matched || matched[4] && matched[1]) {
    throw new TypeError("Invalid time period format");
  }
  const value = parseFloat(matched[2]);
  const unit = matched[3].toLowerCase();
  let numericDate;
  switch (unit) {
    case "sec":
    case "secs":
    case "second":
    case "seconds":
    case "s":
      numericDate = Math.round(value);
      break;
    case "minute":
    case "minutes":
    case "min":
    case "mins":
    case "m":
      numericDate = Math.round(value * minute);
      break;
    case "hour":
    case "hours":
    case "hr":
    case "hrs":
    case "h":
      numericDate = Math.round(value * hour);
      break;
    case "day":
    case "days":
    case "d":
      numericDate = Math.round(value * day);
      break;
    case "week":
    case "weeks":
    case "w":
      numericDate = Math.round(value * week);
      break;
    default:
      numericDate = Math.round(value * year);
      break;
  }
  if (matched[1] === "-" || matched[4] === "ago") {
    return -numericDate;
  }
  return numericDate;
};

// node_modules/jose/dist/browser/lib/jwt_claims_set.js
var normalizeTyp = (value) => value.toLowerCase().replace(/^application\//, "");
var checkAudiencePresence = (audPayload, audOption) => {
  if (typeof audPayload === "string") {
    return audOption.includes(audPayload);
  }
  if (Array.isArray(audPayload)) {
    return audOption.some(Set.prototype.has.bind(new Set(audPayload)));
  }
  return false;
};
var jwt_claims_set_default = (protectedHeader, encodedPayload, options = {}) => {
  let payload;
  try {
    payload = JSON.parse(decoder.decode(encodedPayload));
  } catch {
  }
  if (!isObject4(payload)) {
    throw new JWTInvalid("JWT Claims Set must be a top-level JSON object");
  }
  const { typ } = options;
  if (typ && (typeof protectedHeader.typ !== "string" || normalizeTyp(protectedHeader.typ) !== normalizeTyp(typ))) {
    throw new JWTClaimValidationFailed('unexpected "typ" JWT header value', payload, "typ", "check_failed");
  }
  const { requiredClaims = [], issuer, subject, audience, maxTokenAge } = options;
  const presenceCheck = [...requiredClaims];
  if (maxTokenAge !== void 0)
    presenceCheck.push("iat");
  if (audience !== void 0)
    presenceCheck.push("aud");
  if (subject !== void 0)
    presenceCheck.push("sub");
  if (issuer !== void 0)
    presenceCheck.push("iss");
  for (const claim of new Set(presenceCheck.reverse())) {
    if (!(claim in payload)) {
      throw new JWTClaimValidationFailed(`missing required "${claim}" claim`, payload, claim, "missing");
    }
  }
  if (issuer && !(Array.isArray(issuer) ? issuer : [issuer]).includes(payload.iss)) {
    throw new JWTClaimValidationFailed('unexpected "iss" claim value', payload, "iss", "check_failed");
  }
  if (subject && payload.sub !== subject) {
    throw new JWTClaimValidationFailed('unexpected "sub" claim value', payload, "sub", "check_failed");
  }
  if (audience && !checkAudiencePresence(payload.aud, typeof audience === "string" ? [audience] : audience)) {
    throw new JWTClaimValidationFailed('unexpected "aud" claim value', payload, "aud", "check_failed");
  }
  let tolerance;
  switch (typeof options.clockTolerance) {
    case "string":
      tolerance = secs_default(options.clockTolerance);
      break;
    case "number":
      tolerance = options.clockTolerance;
      break;
    case "undefined":
      tolerance = 0;
      break;
    default:
      throw new TypeError("Invalid clockTolerance option type");
  }
  const { currentDate } = options;
  const now = epoch_default(currentDate || /* @__PURE__ */ new Date());
  if ((payload.iat !== void 0 || maxTokenAge) && typeof payload.iat !== "number") {
    throw new JWTClaimValidationFailed('"iat" claim must be a number', payload, "iat", "invalid");
  }
  if (payload.nbf !== void 0) {
    if (typeof payload.nbf !== "number") {
      throw new JWTClaimValidationFailed('"nbf" claim must be a number', payload, "nbf", "invalid");
    }
    if (payload.nbf > now + tolerance) {
      throw new JWTClaimValidationFailed('"nbf" claim timestamp check failed', payload, "nbf", "check_failed");
    }
  }
  if (payload.exp !== void 0) {
    if (typeof payload.exp !== "number") {
      throw new JWTClaimValidationFailed('"exp" claim must be a number', payload, "exp", "invalid");
    }
    if (payload.exp <= now - tolerance) {
      throw new JWTExpired('"exp" claim timestamp check failed', payload, "exp", "check_failed");
    }
  }
  if (maxTokenAge) {
    const age = now - payload.iat;
    const max = typeof maxTokenAge === "number" ? maxTokenAge : secs_default(maxTokenAge);
    if (age - tolerance > max) {
      throw new JWTExpired('"iat" claim timestamp check failed (too far in the past)', payload, "iat", "check_failed");
    }
    if (age < 0 - tolerance) {
      throw new JWTClaimValidationFailed('"iat" claim timestamp check failed (it should be in the past)', payload, "iat", "check_failed");
    }
  }
  return payload;
};

// node_modules/jose/dist/browser/jwt/verify.js
async function jwtVerify(jwt, key, options) {
  const verified = await compactVerify(jwt, key, options);
  if (verified.protectedHeader.crit?.includes("b64") && verified.protectedHeader.b64 === false) {
    throw new JWTInvalid("JWTs MUST NOT use unencoded payload");
  }
  const payload = jwt_claims_set_default(verified.protectedHeader, verified.payload, options);
  const result = { payload, protectedHeader: verified.protectedHeader };
  if (typeof key === "function") {
    return { ...result, key: verified.key };
  }
  return result;
}

// node_modules/jose/dist/browser/jwks/local.js
function getKtyFromAlg(alg) {
  switch (typeof alg === "string" && alg.slice(0, 2)) {
    case "RS":
    case "PS":
      return "RSA";
    case "ES":
      return "EC";
    case "Ed":
      return "OKP";
    default:
      throw new JOSENotSupported('Unsupported "alg" value for a JSON Web Key Set');
  }
}
function isJWKSLike(jwks) {
  return jwks && typeof jwks === "object" && Array.isArray(jwks.keys) && jwks.keys.every(isJWKLike);
}
function isJWKLike(key) {
  return isObject4(key);
}
function clone(obj) {
  if (typeof structuredClone === "function") {
    return structuredClone(obj);
  }
  return JSON.parse(JSON.stringify(obj));
}
var LocalJWKSet = class {
  constructor(jwks) {
    this._cached = /* @__PURE__ */ new WeakMap();
    if (!isJWKSLike(jwks)) {
      throw new JWKSInvalid("JSON Web Key Set malformed");
    }
    this._jwks = clone(jwks);
  }
  async getKey(protectedHeader, token) {
    const { alg, kid } = { ...protectedHeader, ...token?.header };
    const kty = getKtyFromAlg(alg);
    const candidates = this._jwks.keys.filter((jwk2) => {
      let candidate = kty === jwk2.kty;
      if (candidate && typeof kid === "string") {
        candidate = kid === jwk2.kid;
      }
      if (candidate && typeof jwk2.alg === "string") {
        candidate = alg === jwk2.alg;
      }
      if (candidate && typeof jwk2.use === "string") {
        candidate = jwk2.use === "sig";
      }
      if (candidate && Array.isArray(jwk2.key_ops)) {
        candidate = jwk2.key_ops.includes("verify");
      }
      if (candidate) {
        switch (alg) {
          case "ES256":
            candidate = jwk2.crv === "P-256";
            break;
          case "ES256K":
            candidate = jwk2.crv === "secp256k1";
            break;
          case "ES384":
            candidate = jwk2.crv === "P-384";
            break;
          case "ES512":
            candidate = jwk2.crv === "P-521";
            break;
          case "Ed25519":
            candidate = jwk2.crv === "Ed25519";
            break;
          case "EdDSA":
            candidate = jwk2.crv === "Ed25519" || jwk2.crv === "Ed448";
            break;
        }
      }
      return candidate;
    });
    const { 0: jwk, length } = candidates;
    if (length === 0) {
      throw new JWKSNoMatchingKey();
    }
    if (length !== 1) {
      const error = new JWKSMultipleMatchingKeys();
      const { _cached } = this;
      error[Symbol.asyncIterator] = async function* () {
        for (const jwk2 of candidates) {
          try {
            yield await importWithAlgCache(_cached, jwk2, alg);
          } catch {
          }
        }
      };
      throw error;
    }
    return importWithAlgCache(this._cached, jwk, alg);
  }
};
async function importWithAlgCache(cache2, jwk, alg) {
  const cached = cache2.get(jwk) || cache2.set(jwk, {}).get(jwk);
  if (cached[alg] === void 0) {
    const key = await importJWK({ ...jwk, ext: true }, alg);
    if (key instanceof Uint8Array || key.type !== "public") {
      throw new JWKSInvalid("JSON Web Key Set members must be public keys");
    }
    cached[alg] = key;
  }
  return cached[alg];
}
function createLocalJWKSet(jwks) {
  const set = new LocalJWKSet(jwks);
  const localJWKSet = async (protectedHeader, token) => set.getKey(protectedHeader, token);
  Object.defineProperties(localJWKSet, {
    jwks: {
      value: () => clone(set._jwks),
      enumerable: true,
      configurable: false,
      writable: false
    }
  });
  return localJWKSet;
}

// node_modules/jose/dist/browser/runtime/fetch_jwks.js
var fetchJwks = async (url, timeout, options) => {
  let controller;
  let id;
  let timedOut = false;
  if (typeof AbortController === "function") {
    controller = new AbortController();
    id = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, timeout);
  }
  const response = await fetch(url.href, {
    signal: controller ? controller.signal : void 0,
    redirect: "manual",
    headers: options.headers
  }).catch((err) => {
    if (timedOut)
      throw new JWKSTimeout();
    throw err;
  });
  if (id !== void 0)
    clearTimeout(id);
  if (response.status !== 200) {
    throw new JOSEError("Expected 200 OK from the JSON Web Key Set HTTP response");
  }
  try {
    return await response.json();
  } catch {
    throw new JOSEError("Failed to parse the JSON Web Key Set HTTP response as JSON");
  }
};
var fetch_jwks_default = fetchJwks;

// node_modules/jose/dist/browser/jwks/remote.js
function isCloudflareWorkers() {
  return typeof WebSocketPair !== "undefined" || typeof navigator !== "undefined" && navigator.userAgent === "Cloudflare-Workers" || typeof EdgeRuntime !== "undefined" && EdgeRuntime === "vercel";
}
var USER_AGENT;
if (typeof navigator === "undefined" || !navigator.userAgent?.startsWith?.("Mozilla/5.0 ")) {
  const NAME = "jose";
  const VERSION = "v5.10.0";
  USER_AGENT = `${NAME}/${VERSION}`;
}
var jwksCache = Symbol();
function isFreshJwksCache(input, cacheMaxAge) {
  if (typeof input !== "object" || input === null) {
    return false;
  }
  if (!("uat" in input) || typeof input.uat !== "number" || Date.now() - input.uat >= cacheMaxAge) {
    return false;
  }
  if (!("jwks" in input) || !isObject4(input.jwks) || !Array.isArray(input.jwks.keys) || !Array.prototype.every.call(input.jwks.keys, isObject4)) {
    return false;
  }
  return true;
}
var RemoteJWKSet = class {
  constructor(url, options) {
    if (!(url instanceof URL)) {
      throw new TypeError("url must be an instance of URL");
    }
    this._url = new URL(url.href);
    this._options = { agent: options?.agent, headers: options?.headers };
    this._timeoutDuration = typeof options?.timeoutDuration === "number" ? options?.timeoutDuration : 5e3;
    this._cooldownDuration = typeof options?.cooldownDuration === "number" ? options?.cooldownDuration : 3e4;
    this._cacheMaxAge = typeof options?.cacheMaxAge === "number" ? options?.cacheMaxAge : 6e5;
    if (options?.[jwksCache] !== void 0) {
      this._cache = options?.[jwksCache];
      if (isFreshJwksCache(options?.[jwksCache], this._cacheMaxAge)) {
        this._jwksTimestamp = this._cache.uat;
        this._local = createLocalJWKSet(this._cache.jwks);
      }
    }
  }
  coolingDown() {
    return typeof this._jwksTimestamp === "number" ? Date.now() < this._jwksTimestamp + this._cooldownDuration : false;
  }
  fresh() {
    return typeof this._jwksTimestamp === "number" ? Date.now() < this._jwksTimestamp + this._cacheMaxAge : false;
  }
  async getKey(protectedHeader, token) {
    if (!this._local || !this.fresh()) {
      await this.reload();
    }
    try {
      return await this._local(protectedHeader, token);
    } catch (err) {
      if (err instanceof JWKSNoMatchingKey) {
        if (this.coolingDown() === false) {
          await this.reload();
          return this._local(protectedHeader, token);
        }
      }
      throw err;
    }
  }
  async reload() {
    if (this._pendingFetch && isCloudflareWorkers()) {
      this._pendingFetch = void 0;
    }
    const headers = new Headers(this._options.headers);
    if (USER_AGENT && !headers.has("User-Agent")) {
      headers.set("User-Agent", USER_AGENT);
      this._options.headers = Object.fromEntries(headers.entries());
    }
    this._pendingFetch || (this._pendingFetch = fetch_jwks_default(this._url, this._timeoutDuration, this._options).then((json) => {
      this._local = createLocalJWKSet(json);
      if (this._cache) {
        this._cache.uat = Date.now();
        this._cache.jwks = json;
      }
      this._jwksTimestamp = Date.now();
      this._pendingFetch = void 0;
    }).catch((err) => {
      this._pendingFetch = void 0;
      throw err;
    }));
    await this._pendingFetch;
  }
};
function createRemoteJWKSet(url, options) {
  const set = new RemoteJWKSet(url, options);
  const remoteJWKSet = async (protectedHeader, token) => set.getKey(protectedHeader, token);
  Object.defineProperties(remoteJWKSet, {
    coolingDown: {
      get: () => set.coolingDown(),
      enumerable: true,
      configurable: false
    },
    fresh: {
      get: () => set.fresh(),
      enumerable: true,
      configurable: false
    },
    reload: {
      value: () => set.reload(),
      enumerable: true,
      configurable: false,
      writable: false
    },
    reloading: {
      get: () => !!set._pendingFetch,
      enumerable: true,
      configurable: false
    },
    jwks: {
      value: () => set._local?.jwks(),
      enumerable: true,
      configurable: false,
      writable: false
    }
  });
  return remoteJWKSet;
}

// node_modules/@logto/client/lib/adapter/defaults.js
var defaultClockTolerance = 300;
var verifyIdToken = async (idToken, clientId, issuer, jwks, clockTolerance = defaultClockTolerance) => {
  const result = await jwtVerify(idToken, jwks, { audience: clientId, issuer, clockTolerance });
  if (Math.abs((result.payload.iat ?? 0) - Date.now() / 1e3) > clockTolerance) {
    throw new LogtoError("id_token.invalid_iat");
  }
};
var DefaultJwtVerifier = class {
  constructor(client, clockTolerance = defaultClockTolerance) {
    this.client = client;
    this.clockTolerance = clockTolerance;
  }
  async verifyIdToken(idToken) {
    const { appId } = this.client.logtoConfig;
    const { issuer, jwksUri } = await this.client.getOidcConfig();
    this.getJwtVerifyGetKey ||= createRemoteJWKSet(new URL(jwksUri));
    await verifyIdToken(idToken, appId, issuer, this.getJwtVerifyGetKey, this.clockTolerance);
  }
};

// node_modules/@logto/client/lib/adapter/types.js
var PersistKey;
(function(PersistKey2) {
  PersistKey2["IdToken"] = "idToken";
  PersistKey2["RefreshToken"] = "refreshToken";
  PersistKey2["AccessToken"] = "accessToken";
  PersistKey2["SignInSession"] = "signInSession";
})(PersistKey || (PersistKey = {}));
var CacheKey;
(function(CacheKey2) {
  CacheKey2["OpenidConfig"] = "openidConfiguration";
  CacheKey2["Jwks"] = "jwks";
})(CacheKey || (CacheKey = {}));

// node_modules/@logto/client/lib/adapter/index.js
var ClientAdapterInstance = class {
  /* END OF IMPLEMENTATION */
  constructor(adapter) {
    Object.assign(this, adapter);
  }
  async setStorageItem(key, value) {
    if (!value) {
      await this.storage.removeItem(key);
      return;
    }
    await this.storage.setItem(key, value);
  }
  /**
   * Try to get the string value from the cache and parse as JSON.
   * Return the parsed value if it is an object, return `undefined` otherwise.
   *
   * @param key The cache key to get value from.
   */
  async getCachedObject(key) {
    const cached = await trySafe(async () => {
      const data = await this.unstable_cache?.getItem(key);
      return conditional(data && JSON.parse(data));
    });
    if (cached && typeof cached === "object") {
      return cached;
    }
  }
  /**
   * Try to get the value from the cache first, if it doesn't exist in cache,
   * run the getter function and store the result into cache.
   *
   * @param key The cache key to get value from.
   */
  async getWithCache(key, getter) {
    const cached = await this.getCachedObject(key);
    if (cached) {
      return cached;
    }
    const result = await getter();
    await this.unstable_cache?.setItem(key, JSON.stringify(result));
    return result;
  }
};

// node_modules/@logto/client/lib/errors.js
var logtoClientErrorCodes = Object.freeze({
  "sign_in_session.invalid": "Invalid sign-in session.",
  "sign_in_session.not_found": "Sign-in session not found.",
  not_authenticated: "Not authenticated.",
  fetch_user_info_failed: "Unable to fetch user info. The access token may be invalid.",
  user_cancelled: "The user cancelled the action.",
  missing_scope_organizations: `The \`${UserScope.Organizations}\` scope is required`
});
var LogtoClientError = class extends Error {
  constructor(code, data) {
    super(logtoClientErrorCodes[code]);
    this.name = "LogtoClientError";
    this.code = code;
    this.data = data;
  }
};

// node_modules/@logto/client/lib/types/index.js
var normalizeLogtoConfig = (config) => {
  const { prompt = Prompt.Consent, scopes = [], resources, ...rest } = config;
  const includeReservedScopes = config.includeReservedScopes ?? true;
  return {
    ...rest,
    prompt,
    scopes: includeReservedScopes ? withReservedScopes(scopes).split(" ") : scopes,
    resources: scopes.includes(UserScope.Organizations) ? deduplicate([...resources ?? [], ReservedResource.Organization]) : resources,
    includeReservedScopes
  };
};
var isLogtoSignInSessionItem = (data) => {
  if (!isArbitraryObject(data)) {
    return false;
  }
  return ["redirectUri", "codeVerifier", "state"].every((key) => typeof data[key] === "string");
};
var isLogtoAccessTokenMap = (data) => {
  if (!isArbitraryObject(data)) {
    return false;
  }
  return Object.values(data).every((value) => {
    if (!isArbitraryObject(value)) {
      return false;
    }
    return typeof value.token === "string" && typeof value.scope === "string" && typeof value.expiresAt === "number";
  });
};

// node_modules/@logto/client/lib/utils/index.js
var buildAccessTokenKey = (resource = "", organizationId, scopes = []) => `${scopes.slice().sort().join(" ")}@${resource}${conditionalString(organizationId && `#${organizationId}`)}`;
var getDiscoveryEndpoint = (endpoint) => appendPath(new URL(endpoint), discoveryPath).toString();

// node_modules/@logto/client/lib/utils/memoize.js
function memoize(run) {
  const promiseCache = /* @__PURE__ */ new Map();
  const memoized = async function(...args) {
    const promiseKey = JSON.stringify(args);
    const cachedPromise = promiseCache.get(promiseKey);
    if (cachedPromise) {
      return cachedPromise;
    }
    const promise = (async () => {
      try {
        return await run.apply(this, args);
      } finally {
        promiseCache.delete(promiseKey);
      }
    })();
    promiseCache.set(promiseKey, promise);
    return promise;
  };
  return memoized;
}

// node_modules/@logto/client/lib/utils/once.js
function once2(function_) {
  let called = false;
  let result;
  return function(...args) {
    if (!called) {
      called = true;
      result = function_.apply(this, args);
    }
    return result;
  };
}

// node_modules/@logto/client/lib/client.js
var StandardLogtoClient = class {
  get jwtVerifier() {
    return this.jwtVerifierInstance;
  }
  constructor(logtoConfig, adapter, buildJwtVerifier) {
    this.getOidcConfig = once2(this.#getOidcConfig);
    this.getAccessToken = memoize(this.#getAccessToken);
    this.getOrganizationToken = memoize(this.#getOrganizationToken);
    this.clearAccessToken = memoize(this.#clearAccessToken);
    this.clearAllTokens = memoize(this.#clearAllTokens);
    this.handleSignInCallback = memoize(this.#handleSignInCallback);
    this.accessTokenMap = /* @__PURE__ */ new Map();
    this.logtoConfig = normalizeLogtoConfig(logtoConfig);
    this.adapter = new ClientAdapterInstance(adapter);
    this.jwtVerifierInstance = buildJwtVerifier(this);
    void this.loadAccessTokenMap();
  }
  /**
   * Set the JWT verifier for the client.
   * @param buildJwtVerifier The JWT verifier instance or a function that returns the JWT verifier instance.
   */
  setJwtVerifier(buildJwtVerifier) {
    this.jwtVerifierInstance = typeof buildJwtVerifier === "function" ? buildJwtVerifier(this) : buildJwtVerifier;
  }
  /**
   * Check if the user is authenticated by checking if the ID token exists.
   */
  async isAuthenticated() {
    return Boolean(await this.getIdToken());
  }
  /**
   * Get the Refresh Token from the storage.
   */
  async getRefreshToken() {
    return this.adapter.storage.getItem("refreshToken");
  }
  /**
   * Get the ID Token from the storage. If you want to get the ID Token claims,
   * use {@link getIdTokenClaims} instead.
   */
  async getIdToken() {
    return this.adapter.storage.getItem("idToken");
  }
  /**
   * Get the ID Token claims.
   */
  async getIdTokenClaims() {
    const idToken = await this.getIdToken();
    if (!idToken) {
      throw new LogtoClientError("not_authenticated", "ID token not found");
    }
    return decodeIdToken(idToken);
  }
  /**
   * Get the access token claims for the specified resource.
   *
   * @param resource The resource that the access token is granted for. If not
   * specified, the access token will be used for OpenID Connect or the default
   * resource, as specified in the Logto Console.
   */
  async getAccessTokenClaims(resource) {
    const accessToken2 = await this.getAccessToken(resource);
    return decodeAccessToken(accessToken2);
  }
  /**
   * Get the organization token claims for the specified organization.
   *
   * @param organizationId The ID of the organization that the access token is granted for.
   */
  async getOrganizationTokenClaims(organizationId) {
    const accessToken2 = await this.getOrganizationToken(organizationId);
    return decodeAccessToken(accessToken2);
  }
  /**
   * Get the user information from the Userinfo Endpoint.
   *
   * Note the Userinfo Endpoint will return more claims than the ID Token. See
   * {@link https://docs.logto.io/docs/recipes/integrate-logto/vanilla-js/#fetch-user-information | Fetch user information}
   * for more information.
   *
   * @returns The user information.
   * @throws LogtoClientError if the user is not authenticated.
   */
  async fetchUserInfo() {
    const { userinfoEndpoint } = await this.getOidcConfig();
    const accessToken2 = await this.getAccessToken();
    if (!accessToken2) {
      throw new LogtoClientError("fetch_user_info_failed");
    }
    return fetchUserInfo(userinfoEndpoint, accessToken2, this.adapter.requester);
  }
  async signIn(options, mode, hint) {
    const { redirectUri: redirectUriUrl, postRedirectUri: postRedirectUriUrl, firstScreen, identifiers, interactionMode, loginHint, directSignIn, extraParams, prompt, clearTokens } = typeof options === "string" || options instanceof URL ? {
      redirectUri: options,
      postRedirectUri: void 0,
      firstScreen: void 0,
      identifiers: void 0,
      interactionMode: mode,
      loginHint: hint,
      directSignIn: void 0,
      extraParams: void 0,
      prompt: void 0,
      clearTokens: true
    } : options;
    const redirectUri2 = redirectUriUrl.toString();
    const postRedirectUri = postRedirectUriUrl?.toString();
    const { appId: clientId, prompt: promptViaConfig, resources, scopes, includeReservedScopes } = this.logtoConfig;
    const { authorizationEndpoint } = await this.getOidcConfig();
    const [codeVerifier, state] = await Promise.all([
      this.adapter.generateCodeVerifier(),
      this.adapter.generateState()
    ]);
    const codeChallenge = await this.adapter.generateCodeChallenge(codeVerifier);
    const signInUri = generateSignInUri({
      authorizationEndpoint,
      clientId,
      redirectUri: redirectUri2.toString(),
      codeChallenge,
      state,
      scopes,
      resources,
      prompt: prompt ?? promptViaConfig,
      firstScreen,
      identifiers,
      interactionMode,
      loginHint,
      directSignIn,
      extraParams,
      includeReservedScopes
    });
    await Promise.all([
      this.setSignInSession({ redirectUri: redirectUri2, postRedirectUri, codeVerifier, state }),
      clearTokens === false ? void 0 : this.clearAllTokens()
    ]);
    await this.adapter.navigate(signInUri, { redirectUri: redirectUri2, for: "sign-in" });
  }
  /**
   * Check if the user is redirected from the sign-in page by checking if the
   * current URL matches the redirect URI in the sign-in session.
   *
   * If there's no sign-in session, it will return `false`.
   *
   * @param url The current URL.
   */
  async isSignInRedirected(url) {
    const signInSession = await this.getSignInSession();
    if (!signInSession) {
      return false;
    }
    const { redirectUri: redirectUri2 } = signInSession;
    const { origin: origin2, pathname } = new URL(url);
    return `${origin2}${pathname}` === redirectUri2;
  }
  /**
   * Start the sign-out flow with the specified redirect URI. The URI must be
   * registered in the Logto Console.
   *
   * It will also revoke all the tokens and clean up the storage.
   *
   * The user will be redirected that URI after the sign-out flow is completed.
   * If the `postLogoutRedirectUri` is not specified, the user will be redirected
   * to a default page.
   */
  async signOut(postLogoutRedirectUri2) {
    const { appId: clientId } = this.logtoConfig;
    const { endSessionEndpoint, revocationEndpoint } = await this.getOidcConfig();
    const refreshToken = await this.getRefreshToken();
    if (refreshToken) {
      try {
        await revoke(revocationEndpoint, clientId, refreshToken, this.adapter.requester);
      } catch {
      }
    }
    const url = generateSignOutUri({
      endSessionEndpoint,
      postLogoutRedirectUri: postLogoutRedirectUri2,
      clientId
    });
    await this.clearAllTokens();
    await this.adapter.navigate(url, { redirectUri: postLogoutRedirectUri2, for: "sign-out" });
  }
  async getSignInSession() {
    const jsonItem = await this.adapter.storage.getItem("signInSession");
    if (!jsonItem) {
      return null;
    }
    const item = JSON.parse(jsonItem);
    if (!isLogtoSignInSessionItem(item)) {
      throw new LogtoClientError("sign_in_session.invalid");
    }
    return item;
  }
  async setSignInSession(value) {
    return this.adapter.setStorageItem(PersistKey.SignInSession, value && JSON.stringify(value));
  }
  async setIdToken(value) {
    return this.adapter.setStorageItem(PersistKey.IdToken, value);
  }
  async setRefreshToken(value) {
    return this.adapter.setStorageItem(PersistKey.RefreshToken, value);
  }
  async getAccessTokenByRefreshToken(resource, organizationId) {
    const currentRefreshToken = await this.getRefreshToken();
    if (!currentRefreshToken) {
      throw new LogtoClientError("not_authenticated", "Refresh token not found");
    }
    const accessTokenKey = buildAccessTokenKey(resource, organizationId);
    const { appId: clientId } = this.logtoConfig;
    const { tokenEndpoint } = await this.getOidcConfig();
    const requestedAt = Math.round(Date.now() / 1e3);
    const { accessToken: accessToken2, refreshToken, idToken, scope, expiresIn } = await fetchTokenByRefreshToken({
      clientId,
      tokenEndpoint,
      refreshToken: currentRefreshToken,
      resource,
      organizationId
    }, this.adapter.requester);
    this.accessTokenMap.set(accessTokenKey, {
      token: accessToken2,
      scope,
      /** The `expiresAt` variable provides an approximate estimation of the actual `exp` property
       * in the token claims. It is utilized by the client to determine if the cached access token
       * has expired and when a new access token should be requested.
       */
      expiresAt: requestedAt + expiresIn
    });
    await this.saveAccessTokenMap();
    if (refreshToken) {
      await this.setRefreshToken(refreshToken);
    }
    if (idToken) {
      await this.jwtVerifier.verifyIdToken(idToken);
      await this.setIdToken(idToken);
    }
    return accessToken2;
  }
  async saveAccessTokenMap() {
    const data = {};
    for (const [key, accessToken2] of this.accessTokenMap.entries()) {
      data[key] = accessToken2;
    }
    await this.adapter.storage.setItem("accessToken", JSON.stringify(data));
  }
  async loadAccessTokenMap() {
    const raw = await this.adapter.storage.getItem("accessToken");
    if (!raw) {
      return;
    }
    try {
      const json = JSON.parse(raw);
      if (!isLogtoAccessTokenMap(json)) {
        return;
      }
      this.accessTokenMap.clear();
      for (const [key, accessToken2] of Object.entries(json)) {
        this.accessTokenMap.set(key, accessToken2);
      }
    } catch (error) {
      console.warn(error);
    }
  }
  async #getOidcConfig() {
    return this.adapter.getWithCache(CacheKey.OpenidConfig, async () => {
      return fetchOidcConfig(getDiscoveryEndpoint(this.logtoConfig.endpoint), this.adapter.requester);
    });
  }
  async #getAccessToken(resource, organizationId) {
    if (!await this.isAuthenticated()) {
      throw new LogtoClientError("not_authenticated");
    }
    const accessTokenKey = buildAccessTokenKey(resource, organizationId);
    const accessToken2 = this.accessTokenMap.get(accessTokenKey);
    if (accessToken2 && accessToken2.expiresAt > Date.now() / 1e3) {
      return accessToken2.token;
    }
    if (accessToken2) {
      this.accessTokenMap.delete(accessTokenKey);
    }
    return this.getAccessTokenByRefreshToken(resource, organizationId);
  }
  async #getOrganizationToken(organizationId) {
    if (!this.logtoConfig.scopes?.includes(UserScope.Organizations)) {
      throw new LogtoClientError("missing_scope_organizations");
    }
    return this.getAccessToken(void 0, organizationId);
  }
  async #clearAccessToken() {
    this.accessTokenMap.clear();
    await this.adapter.storage.removeItem("accessToken");
  }
  async #clearAllTokens() {
    await Promise.all([this.setRefreshToken(null), this.setIdToken(null), this.clearAccessToken()]);
  }
  async #handleSignInCallback(callbackUri) {
    const signInSession = await this.getSignInSession();
    if (!signInSession) {
      throw new LogtoClientError("sign_in_session.not_found");
    }
    const { redirectUri: redirectUri2, postRedirectUri, state, codeVerifier } = signInSession;
    const code = verifyAndParseCodeFromCallbackUri(callbackUri, redirectUri2, state);
    const accessTokenKey = buildAccessTokenKey();
    const { appId: clientId } = this.logtoConfig;
    const { tokenEndpoint } = await this.getOidcConfig();
    const requestedAt = Math.round(Date.now() / 1e3);
    const { idToken, refreshToken, accessToken: accessToken2, scope, expiresIn } = await fetchTokenByAuthorizationCode({
      clientId,
      tokenEndpoint,
      redirectUri: redirectUri2,
      codeVerifier,
      code
    }, this.adapter.requester);
    await this.jwtVerifier.verifyIdToken(idToken);
    await this.setRefreshToken(refreshToken ?? null);
    await this.setIdToken(idToken);
    this.accessTokenMap.set(accessTokenKey, {
      token: accessToken2,
      scope,
      /** The `expiresAt` variable provides an approximate estimation of the actual `exp` property
       * in the token claims. It is utilized by the client to determine if the cached access token
       * has expired and when a new access token should be requested.
       */
      expiresAt: requestedAt + expiresIn
    });
    await this.saveAccessTokenMap();
    await this.setSignInSession(null);
    if (postRedirectUri) {
      await this.adapter.navigate(postRedirectUri, { for: "post-sign-in" });
    }
  }
};

// node_modules/@logto/client/lib/utils/requester.js
var createRequester = (fetchFunction) => {
  return async (...args) => {
    const response = await fetchFunction(...args);
    if (!response.ok) {
      const cloned = response.clone();
      const responseJson = await response.json();
      console.error(`Logto requester error: [status=${response.status}]`, responseJson);
      if (!isLogtoRequestErrorJson(responseJson)) {
        throw new LogtoError("unexpected_response_error", responseJson);
      }
      const { code, message: message2 } = responseJson;
      throw new LogtoRequestError(code, message2, cloned);
    }
    return response.json();
  };
};

// node_modules/@logto/client/lib/index.js
var LogtoClient = class extends StandardLogtoClient {
  constructor(logtoConfig, adapter, buildJwtVerifier) {
    super(logtoConfig, adapter, buildJwtVerifier ?? ((client) => new DefaultJwtVerifier(client)));
  }
};

// node_modules/@logto/browser/lib/cache.js
var keyPrefix = `logto_cache`;
var CacheStorage = class {
  constructor(appId) {
    this.appId = appId;
  }
  getKey(item) {
    if (item === void 0) {
      return `${keyPrefix}:${this.appId}`;
    }
    return `${keyPrefix}:${this.appId}:${item}`;
  }
  async getItem(key) {
    return sessionStorage.getItem(this.getKey(key));
  }
  async setItem(key, value) {
    sessionStorage.setItem(this.getKey(key), value);
  }
  async removeItem(key) {
    sessionStorage.removeItem(`${this.getKey(key)}`);
  }
};

// node_modules/@logto/browser/lib/storage.js
var keyPrefix2 = `logto`;
var BrowserStorage = class {
  constructor(appId) {
    this.appId = appId;
  }
  getKey(item) {
    if (item === void 0) {
      return `${keyPrefix2}:${this.appId}`;
    }
    return `${keyPrefix2}:${this.appId}:${item}`;
  }
  async getItem(key) {
    if (typeof window === "undefined") {
      return null;
    }
    if (key === "signInSession") {
      return sessionStorage.getItem(this.getKey(key)) ?? sessionStorage.getItem(this.getKey());
    }
    return localStorage.getItem(this.getKey(key));
  }
  async setItem(key, value) {
    if (typeof window === "undefined") {
      return;
    }
    if (key === "signInSession") {
      sessionStorage.setItem(this.getKey(key), value);
      return;
    }
    localStorage.setItem(this.getKey(key), value);
  }
  async removeItem(key) {
    if (typeof window === "undefined") {
      return;
    }
    if (key === "signInSession") {
      sessionStorage.removeItem(this.getKey(key));
      return;
    }
    localStorage.removeItem(this.getKey(key));
  }
};

// node_modules/js-base64/base64.mjs
var _TD = typeof TextDecoder === "function" ? new TextDecoder("utf-8", { ignoreBOM: true }) : void 0;
var _TE = typeof TextEncoder === "function" ? new TextEncoder() : void 0;
var b64ch = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=";
var b64chs = Array.prototype.slice.call(b64ch);
var b64tab = ((a) => {
  let tab = {};
  a.forEach((c, i) => tab[c] = i);
  return tab;
})(b64chs);
var b64re = /^(?:[A-Za-z\d+\/]{4})*?(?:[A-Za-z\d+\/]{2}(?:==)?|[A-Za-z\d+\/]{3}=?)?$/;
var _fromCC = String.fromCharCode.bind(String);
var _U8Afrom = typeof Uint8Array.from === "function" ? Uint8Array.from.bind(Uint8Array) : (it) => new Uint8Array(Array.prototype.slice.call(it, 0));
var _mkUriSafe = (src) => src.replace(/=/g, "").replace(/[+\/]/g, (m0) => m0 == "+" ? "-" : "_");
var _tidyB64 = (s) => s.replace(/[^A-Za-z0-9\+\/]/g, "");
var btoaPolyfill = (bin) => {
  let u32, c0, c1, c2, asc = "";
  const pad = bin.length % 3;
  for (let i = 0; i < bin.length; ) {
    if ((c0 = bin.charCodeAt(i++)) > 255 || (c1 = bin.charCodeAt(i++)) > 255 || (c2 = bin.charCodeAt(i++)) > 255)
      throw new TypeError("invalid character found");
    u32 = c0 << 16 | c1 << 8 | c2;
    asc += b64chs[u32 >> 18 & 63] + b64chs[u32 >> 12 & 63] + b64chs[u32 >> 6 & 63] + b64chs[u32 & 63];
  }
  return pad ? asc.slice(0, pad - 3) + "===".substring(pad) : asc;
};
var _btoa = typeof btoa === "function" ? (bin) => btoa(bin) : btoaPolyfill;
var _fromUint8Array = typeof Uint8Array.prototype.toBase64 === "function" ? (u8a) => u8a.toBase64() : (u8a) => {
  const maxargs = 4096;
  let strs = [];
  for (let i = 0, l = u8a.length; i < l; i += maxargs) {
    strs.push(_fromCC.apply(null, u8a.subarray(i, i + maxargs)));
  }
  return _btoa(strs.join(""));
};
var fromUint8Array = (u8a, urlsafe = false) => urlsafe ? _mkUriSafe(_fromUint8Array(u8a)) : _fromUint8Array(u8a);
var atobPolyfill = (asc) => {
  asc = asc.replace(/\s+/g, "");
  if (!b64re.test(asc))
    throw new TypeError("malformed base64.");
  asc += "==".slice(2 - (asc.length & 3));
  let u24, r1, r2;
  let binArray = [];
  for (let i = 0; i < asc.length; ) {
    u24 = b64tab[asc.charAt(i++)] << 18 | b64tab[asc.charAt(i++)] << 12 | (r1 = b64tab[asc.charAt(i++)]) << 6 | (r2 = b64tab[asc.charAt(i++)]);
    if (r1 === 64) {
      binArray.push(_fromCC(u24 >> 16 & 255));
    } else if (r2 === 64) {
      binArray.push(_fromCC(u24 >> 16 & 255, u24 >> 8 & 255));
    } else {
      binArray.push(_fromCC(u24 >> 16 & 255, u24 >> 8 & 255, u24 & 255));
    }
  }
  return binArray.join("");
};
var _atob = typeof atob === "function" ? (asc) => atob(_tidyB64(asc)) : atobPolyfill;
var _toUint8Array = typeof Uint8Array.fromBase64 === "function" ? (a) => Uint8Array.fromBase64(a) : (a) => _U8Afrom(_atob(a).split("").map((c) => c.charCodeAt(0)));

// node_modules/@logto/browser/lib/utils/generators.js
var generateRandomString = (length = 64) => fromUint8Array(crypto.getRandomValues(new Uint8Array(length)), true);
var generateState = () => generateRandomString();
var generateCodeVerifier = () => generateRandomString();
var generateCodeChallenge = async (codeVerifier) => {
  if (crypto.subtle === void 0) {
    throw new LogtoError("crypto_subtle_unavailable");
  }
  const encodedCodeVerifier = new TextEncoder().encode(codeVerifier);
  const codeChallenge = new Uint8Array(await crypto.subtle.digest("SHA-256", encodedCodeVerifier));
  return fromUint8Array(codeChallenge, true);
};

// node_modules/@logto/browser/lib/index.js
var navigate = (url) => {
  window.location.assign(url);
};
var LogtoClient2 = class extends LogtoClient {
  /**
   * @param config The configuration object for the client.
   * @param [unstable_enableCache=false] Whether to enable cache for well-known data.
   * Use sessionStorage by default.
   */
  constructor(config, unstable_enableCache = false) {
    const requester = createRequester(fetch);
    super(config, {
      requester,
      navigate,
      storage: new BrowserStorage(config.appId),
      unstable_cache: conditional(unstable_enableCache && new CacheStorage(config.appId)),
      generateCodeChallenge,
      generateCodeVerifier,
      generateState
    });
  }
};

// node_modules/openapi-fetch/dist/index.mjs
var PATH_PARAM_RE = /\{[^{}]+\}/g;
var supportsRequestInitExt = () => {
  return typeof process === "object" && Number.parseInt(process?.versions?.node?.substring(0, 2)) >= 18 && process.versions.undici;
};
function randomID() {
  return Math.random().toString(36).slice(2, 11);
}
function createClient(clientOptions) {
  let {
    baseUrl = "",
    Request: CustomRequest = globalThis.Request,
    fetch: baseFetch = globalThis.fetch,
    querySerializer: globalQuerySerializer,
    bodySerializer: globalBodySerializer,
    pathSerializer: globalPathSerializer,
    headers: baseHeaders,
    requestInitExt = void 0,
    ...baseOptions
  } = { ...clientOptions };
  requestInitExt = supportsRequestInitExt() ? requestInitExt : void 0;
  baseUrl = removeTrailingSlash(baseUrl);
  const globalMiddlewares = [];
  async function coreFetch(schemaPath, fetchOptions) {
    const {
      baseUrl: localBaseUrl,
      fetch: fetch2 = baseFetch,
      Request: Request2 = CustomRequest,
      headers,
      params = {},
      parseAs = "json",
      querySerializer: requestQuerySerializer,
      bodySerializer = globalBodySerializer ?? defaultBodySerializer,
      pathSerializer: requestPathSerializer,
      body,
      middleware: requestMiddlewares = [],
      ...init
    } = fetchOptions || {};
    let finalBaseUrl = baseUrl;
    if (localBaseUrl) {
      finalBaseUrl = removeTrailingSlash(localBaseUrl) ?? baseUrl;
    }
    let querySerializer = typeof globalQuerySerializer === "function" ? globalQuerySerializer : createQuerySerializer(globalQuerySerializer);
    if (requestQuerySerializer) {
      querySerializer = typeof requestQuerySerializer === "function" ? requestQuerySerializer : createQuerySerializer({
        ...typeof globalQuerySerializer === "object" ? globalQuerySerializer : {},
        ...requestQuerySerializer
      });
    }
    const pathSerializer = requestPathSerializer || globalPathSerializer || defaultPathSerializer;
    const serializedBody = body === void 0 ? void 0 : bodySerializer(
      body,
      // Note: we declare mergeHeaders() both here and below because it’s a bit of a chicken-or-egg situation:
      // bodySerializer() needs all headers so we aren’t dropping ones set by the user, however,
      // the result of this ALSO sets the lowest-priority content-type header. So we re-merge below,
      // setting the content-type at the very beginning to be overwritten.
      // Lastly, based on the way headers work, it’s not a simple “present-or-not” check becauase null intentionally un-sets headers.
      mergeHeaders(baseHeaders, headers, params.header)
    );
    const finalHeaders = mergeHeaders(
      // with no body, we should not to set Content-Type
      serializedBody === void 0 || // if serialized body is FormData; browser will correctly set Content-Type & boundary expression
      serializedBody instanceof FormData ? {} : {
        "Content-Type": "application/json"
      },
      baseHeaders,
      headers,
      params.header
    );
    const finalMiddlewares = [...globalMiddlewares, ...requestMiddlewares];
    const requestInit = {
      redirect: "follow",
      ...baseOptions,
      ...init,
      body: serializedBody,
      headers: finalHeaders
    };
    let id;
    let options;
    let request = new Request2(
      createFinalURL(schemaPath, { baseUrl: finalBaseUrl, params, querySerializer, pathSerializer }),
      requestInit
    );
    let response;
    for (const key in init) {
      if (!(key in request)) {
        request[key] = init[key];
      }
    }
    if (finalMiddlewares.length) {
      id = randomID();
      options = Object.freeze({
        baseUrl: finalBaseUrl,
        fetch: fetch2,
        parseAs,
        querySerializer,
        bodySerializer,
        pathSerializer
      });
      for (const m of finalMiddlewares) {
        if (m && typeof m === "object" && typeof m.onRequest === "function") {
          const result = await m.onRequest({
            request,
            schemaPath,
            params,
            options,
            id
          });
          if (result) {
            if (result instanceof Request2) {
              request = result;
            } else if (result instanceof Response) {
              response = result;
              break;
            } else {
              throw new Error("onRequest: must return new Request() or Response() when modifying the request");
            }
          }
        }
      }
    }
    if (!response) {
      try {
        response = await fetch2(request, requestInitExt);
      } catch (error2) {
        let errorAfterMiddleware = error2;
        if (finalMiddlewares.length) {
          for (let i = finalMiddlewares.length - 1; i >= 0; i--) {
            const m = finalMiddlewares[i];
            if (m && typeof m === "object" && typeof m.onError === "function") {
              const result = await m.onError({
                request,
                error: errorAfterMiddleware,
                schemaPath,
                params,
                options,
                id
              });
              if (result) {
                if (result instanceof Response) {
                  errorAfterMiddleware = void 0;
                  response = result;
                  break;
                }
                if (result instanceof Error) {
                  errorAfterMiddleware = result;
                  continue;
                }
                throw new Error("onError: must return new Response() or instance of Error");
              }
            }
          }
        }
        if (errorAfterMiddleware) {
          throw errorAfterMiddleware;
        }
      }
      if (finalMiddlewares.length) {
        for (let i = finalMiddlewares.length - 1; i >= 0; i--) {
          const m = finalMiddlewares[i];
          if (m && typeof m === "object" && typeof m.onResponse === "function") {
            const result = await m.onResponse({
              request,
              response,
              schemaPath,
              params,
              options,
              id
            });
            if (result) {
              if (!(result instanceof Response)) {
                throw new Error("onResponse: must return new Response() when modifying the response");
              }
              response = result;
            }
          }
        }
      }
    }
    const contentLength = response.headers.get("Content-Length");
    if (response.status === 204 || request.method === "HEAD" || contentLength === "0" && !response.headers.get("Transfer-Encoding")?.includes("chunked")) {
      return response.ok ? { data: void 0, response } : { error: void 0, response };
    }
    if (response.ok) {
      const getResponseData = async () => {
        if (parseAs === "stream") {
          return response.body;
        }
        if (parseAs === "json" && !contentLength) {
          const raw = await response.text();
          return raw ? JSON.parse(raw) : void 0;
        }
        return await response[parseAs]();
      };
      return { data: await getResponseData(), response };
    }
    let error = await response.text();
    try {
      error = JSON.parse(error);
    } catch {
    }
    return { error, response };
  }
  return {
    request(method, url, init) {
      return coreFetch(url, { ...init, method: method.toUpperCase() });
    },
    /** Call a GET endpoint */
    GET(url, init) {
      return coreFetch(url, { ...init, method: "GET" });
    },
    /** Call a PUT endpoint */
    PUT(url, init) {
      return coreFetch(url, { ...init, method: "PUT" });
    },
    /** Call a POST endpoint */
    POST(url, init) {
      return coreFetch(url, { ...init, method: "POST" });
    },
    /** Call a DELETE endpoint */
    DELETE(url, init) {
      return coreFetch(url, { ...init, method: "DELETE" });
    },
    /** Call a OPTIONS endpoint */
    OPTIONS(url, init) {
      return coreFetch(url, { ...init, method: "OPTIONS" });
    },
    /** Call a HEAD endpoint */
    HEAD(url, init) {
      return coreFetch(url, { ...init, method: "HEAD" });
    },
    /** Call a PATCH endpoint */
    PATCH(url, init) {
      return coreFetch(url, { ...init, method: "PATCH" });
    },
    /** Call a TRACE endpoint */
    TRACE(url, init) {
      return coreFetch(url, { ...init, method: "TRACE" });
    },
    /** Register middleware */
    use(...middleware) {
      for (const m of middleware) {
        if (!m) {
          continue;
        }
        if (typeof m !== "object" || !("onRequest" in m || "onResponse" in m || "onError" in m)) {
          throw new Error("Middleware must be an object with one of `onRequest()`, `onResponse() or `onError()`");
        }
        globalMiddlewares.push(m);
      }
    },
    /** Unregister middleware */
    eject(...middleware) {
      for (const m of middleware) {
        const i = globalMiddlewares.indexOf(m);
        if (i !== -1) {
          globalMiddlewares.splice(i, 1);
        }
      }
    }
  };
}
function serializePrimitiveParam(name, value, options) {
  if (value === void 0 || value === null) {
    return "";
  }
  if (typeof value === "object") {
    throw new Error(
      "Deeply-nested arrays/objects aren\u2019t supported. Provide your own `querySerializer()` to handle these."
    );
  }
  return `${name}=${options?.allowReserved === true ? value : encodeURIComponent(value)}`;
}
function serializeObjectParam(name, value, options) {
  if (!value || typeof value !== "object") {
    return "";
  }
  const values = [];
  const joiner = {
    simple: ",",
    label: ".",
    matrix: ";"
  }[options.style] || "&";
  if (options.style !== "deepObject" && options.explode === false) {
    for (const k in value) {
      values.push(k, options.allowReserved === true ? value[k] : encodeURIComponent(value[k]));
    }
    const final2 = values.join(",");
    switch (options.style) {
      case "form": {
        return `${name}=${final2}`;
      }
      case "label": {
        return `.${final2}`;
      }
      case "matrix": {
        return `;${name}=${final2}`;
      }
      default: {
        return final2;
      }
    }
  }
  for (const k in value) {
    const finalName = options.style === "deepObject" ? `${name}[${k}]` : k;
    values.push(serializePrimitiveParam(finalName, value[k], options));
  }
  const final = values.join(joiner);
  return options.style === "label" || options.style === "matrix" ? `${joiner}${final}` : final;
}
function serializeArrayParam(name, value, options) {
  if (!Array.isArray(value)) {
    return "";
  }
  if (options.explode === false) {
    const joiner2 = { form: ",", spaceDelimited: "%20", pipeDelimited: "|" }[options.style] || ",";
    const final = (options.allowReserved === true ? value : value.map((v) => encodeURIComponent(v))).join(joiner2);
    switch (options.style) {
      case "simple": {
        return final;
      }
      case "label": {
        return `.${final}`;
      }
      case "matrix": {
        return `;${name}=${final}`;
      }
      // case "spaceDelimited":
      // case "pipeDelimited":
      default: {
        return `${name}=${final}`;
      }
    }
  }
  const joiner = { simple: ",", label: ".", matrix: ";" }[options.style] || "&";
  const values = [];
  for (const v of value) {
    if (options.style === "simple" || options.style === "label") {
      values.push(options.allowReserved === true ? v : encodeURIComponent(v));
    } else {
      values.push(serializePrimitiveParam(name, v, options));
    }
  }
  return options.style === "label" || options.style === "matrix" ? `${joiner}${values.join(joiner)}` : values.join(joiner);
}
function createQuerySerializer(options) {
  return function querySerializer(queryParams) {
    const search = [];
    if (queryParams && typeof queryParams === "object") {
      for (const name in queryParams) {
        const value = queryParams[name];
        if (value === void 0 || value === null) {
          continue;
        }
        if (Array.isArray(value)) {
          if (value.length === 0) {
            continue;
          }
          search.push(
            serializeArrayParam(name, value, {
              style: "form",
              explode: true,
              ...options?.array,
              allowReserved: options?.allowReserved || false
            })
          );
          continue;
        }
        if (typeof value === "object") {
          search.push(
            serializeObjectParam(name, value, {
              style: "deepObject",
              explode: true,
              ...options?.object,
              allowReserved: options?.allowReserved || false
            })
          );
          continue;
        }
        search.push(serializePrimitiveParam(name, value, options));
      }
    }
    return search.join("&");
  };
}
function defaultPathSerializer(pathname, pathParams) {
  let nextURL = pathname;
  for (const match of pathname.match(PATH_PARAM_RE) ?? []) {
    let name = match.substring(1, match.length - 1);
    let explode = false;
    let style = "simple";
    if (name.endsWith("*")) {
      explode = true;
      name = name.substring(0, name.length - 1);
    }
    if (name.startsWith(".")) {
      style = "label";
      name = name.substring(1);
    } else if (name.startsWith(";")) {
      style = "matrix";
      name = name.substring(1);
    }
    if (!pathParams || pathParams[name] === void 0 || pathParams[name] === null) {
      continue;
    }
    const value = pathParams[name];
    if (Array.isArray(value)) {
      nextURL = nextURL.replace(match, serializeArrayParam(name, value, { style, explode }));
      continue;
    }
    if (typeof value === "object") {
      nextURL = nextURL.replace(match, serializeObjectParam(name, value, { style, explode }));
      continue;
    }
    if (style === "matrix") {
      nextURL = nextURL.replace(match, `;${serializePrimitiveParam(name, value)}`);
      continue;
    }
    nextURL = nextURL.replace(match, style === "label" ? `.${encodeURIComponent(value)}` : encodeURIComponent(value));
  }
  return nextURL;
}
function defaultBodySerializer(body, headers) {
  if (body instanceof FormData) {
    return body;
  }
  if (headers) {
    const contentType = headers.get instanceof Function ? headers.get("Content-Type") ?? headers.get("content-type") : headers["Content-Type"] ?? headers["content-type"];
    if (contentType === "application/x-www-form-urlencoded") {
      return new URLSearchParams(body).toString();
    }
  }
  return JSON.stringify(body);
}
function createFinalURL(pathname, options) {
  let finalURL = `${options.baseUrl}${pathname}`;
  if (options.params?.path) {
    finalURL = options.pathSerializer(finalURL, options.params.path);
  }
  let search = options.querySerializer(options.params.query ?? {});
  if (search.startsWith("?")) {
    search = search.substring(1);
  }
  if (search) {
    finalURL += `?${search}`;
  }
  return finalURL;
}
function mergeHeaders(...allHeaders) {
  const finalHeaders = new Headers();
  for (const h of allHeaders) {
    if (!h || typeof h !== "object") {
      continue;
    }
    const iterator = h instanceof Headers ? h.entries() : Object.entries(h);
    for (const [k, v] of iterator) {
      if (v === null) {
        finalHeaders.delete(k);
      } else if (Array.isArray(v)) {
        for (const v2 of v) {
          finalHeaders.append(k, v2);
        }
      } else if (v !== void 0) {
        finalHeaders.set(k, v);
      }
    }
  }
  return finalHeaders;
}
function removeTrailingSlash(url) {
  if (url.endsWith("/")) {
    return url.substring(0, url.length - 1);
  }
  return url;
}

// src/api.ts
var CATALOG = "/catalog";
var STOREFRONT = "/storefront";
var accessToken = "";
function setAccessToken(token) {
  accessToken = token;
}
function devUser() {
  const el = document.getElementById("user");
  return el?.value.trim() || "alice";
}
var auth = {
  async onRequest({ request }) {
    const headers = new Headers(request.headers);
    if (headers.has("X-User")) {
      headers.delete("Authorization");
      return new Request(request, { headers });
    }
    if (accessToken) {
      headers.set("Authorization", `Bearer ${accessToken}`);
    } else {
      headers.set("X-User", devUser());
    }
    return new Request(request, { headers });
  }
};
var catalog = createClient({ baseUrl: CATALOG });
var storefront = createClient({ baseUrl: STOREFRONT });
catalog.use(auth);
storefront.use(auth);
function unwrap(res) {
  if (res.response.ok && res.data !== void 0) {
    return res.data;
  }
  const err = res.error;
  const msg = err?.detail || err?.title || err?.message || res.response.statusText || "request failed";
  throw new Error(`${res.response.status} ${msg}`);
}

// src/app.ts
var API_RESOURCE = "https://fxkit.showcase/api";
var LOGTO_ENDPOINT = "https://10.170.34.223:3001/";
var LOGTO_APP_ID = "2h3jba3gurf7r3vsvqydb";
var $ = (id) => {
  const el = document.getElementById(id);
  if (!el) throw new Error(`#${id} missing`);
  return el;
};
var origin = window.location.origin;
var redirectUri = `${origin}/callback`;
var postLogoutRedirectUri = `${origin}/`;
var logto = new LogtoClient2({
  endpoint: LOGTO_ENDPOINT,
  appId: LOGTO_APP_ID,
  resources: [API_RESOURCE],
  scopes: ["openid", "profile", "email", "offline_access"]
});
var bindAdminAttempted = false;
function escapeHtml(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}
async function refreshAccessToken() {
  setAccessToken("");
  if (!await logto.isAuthenticated()) {
    return;
  }
  setAccessToken(await logto.getAccessToken(API_RESOURCE));
}
async function refreshAuthUi() {
  const signedIn = await logto.isAuthenticated();
  $("sign-in").hidden = signedIn;
  $("sign-out").hidden = !signedIn;
  $("bind-admin").hidden = !signedIn;
  const status = $("auth-status");
  if (!signedIn) {
    status.textContent = `\u672A\u767B\u5F55 \xB7 API \u8D70 X-User\u3002Logto \u987B\u767B\u8BB0\uFF1A${redirectUri}`;
    return;
  }
  try {
    const claims = await logto.getIdTokenClaims();
    const who = claims.email || claims.username || claims.sub;
    status.textContent = `Logto ${who} \xB7 sub=${claims.sub}`;
    status.title = JSON.stringify(claims, null, 2);
  } catch (err) {
    status.textContent = `\u5DF2\u767B\u5F55\u4F46\u8BFB claims \u5931\u8D25\uFF1A${err instanceof Error ? err.message : err}`;
  }
}
async function bindJwtAdmin() {
  const claims = await logto.getIdTokenClaims();
  const sub = claims.sub;
  if (!sub) throw new Error("id token \u6CA1\u6709 sub");
  unwrap(
    await catalog.PUT("/authz/subjects/{id}/roles", {
      params: { path: { id: sub } },
      body: { roles: ["admin"] },
      headers: { "X-User": "alice" }
    })
  );
  return sub;
}
async function refreshList() {
  const status = $("list-status");
  const ul = $("articles");
  status.textContent = "\u52A0\u8F7D\u4E2D\u2026";
  try {
    const data = unwrap(
      await catalog.GET("/articles", {
        params: {
          query: { replyWithCount: true, sortBy: "id", sortDirection: "desc", limit: 20 }
        }
      })
    );
    const items = data.items ?? [];
    status.textContent = `\u5171 ${data.total ?? items.length} \u6761\uFF08\u7ECF Traefik ${CATALOG}/articles\uFF09`;
    ul.innerHTML = items.map(
      (a) => `<li><span>#${a.id} ${escapeHtml(a.title)}</span><span class="muted">${escapeHtml(a.status)} \xB7 ${escapeHtml(a.author || "")}</span></li>`
    ).join("");
    if (!items.length) ul.innerHTML = "<li class='muted'>\u6682\u65E0\u6587\u7AE0</li>";
  } catch (err) {
    const msg = String(err instanceof Error ? err.message : err);
    if (accessToken && /403/.test(msg) && !bindAdminAttempted) {
      bindAdminAttempted = true;
      status.textContent = "JWT \u65E0\u89D2\u8272\uFF0C\u6B63\u5728\u7ED1\u5B9A admin\u2026";
      try {
        const sub = await bindJwtAdmin();
        $("auth-status").textContent = `\u5DF2\u628A ${sub} \u7ED1\u5230 admin`;
        await refreshList();
        return;
      } catch (bindErr) {
        status.textContent = `403\uFF0C\u81EA\u52A8\u7ED1\u5B9A\u5931\u8D25\uFF1A${bindErr instanceof Error ? bindErr.message : bindErr}`;
        ul.innerHTML = "";
        return;
      }
    }
    status.textContent = msg;
    ul.innerHTML = "";
  }
}
async function pollIndexed(id) {
  const status = $("list-status");
  const pathId = String(id);
  for (let i = 0; i < 16; i++) {
    try {
      const row = unwrap(await catalog.GET("/articles/{id}", { params: { path: { id: pathId } } }));
      if (row.status === "indexed") {
        status.textContent = `#${id} \u5DF2 indexed\uFF08outbox \u2192 Hatchet \u2192 inbox\uFF09`;
        await refreshList();
        return;
      }
    } catch {
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  status.textContent = `#${id} \u8FD8\u6CA1 indexed\uFF08\u770B Hatchet worker / token\uFF09`;
}
function bindPageHandlers() {
  $("sign-in").addEventListener("click", () => {
    void logto.signIn(redirectUri);
  });
  $("sign-out").addEventListener("click", () => {
    void logto.signOut(postLogoutRedirectUri);
  });
  $("bind-admin").addEventListener("click", async () => {
    try {
      const sub = await bindJwtAdmin();
      $("auth-status").textContent = `\u5DF2\u628A ${sub} \u7ED1\u5230 admin\uFF0C\u518D\u62C9\u4E00\u6B21\u5217\u8868`;
      await refreshAccessToken();
      await refreshList();
    } catch (err) {
      $("auth-status").textContent = String(err instanceof Error ? err.message : err);
    }
  });
  $("create").addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      const created = unwrap(
        await catalog.POST("/articles", {
          body: {
            title: $("title").value.trim(),
            body: $("body").value
          }
        })
      );
      $("title").value = "";
      $("body").value = "";
      await refreshList();
      if (created.id) {
        void pollIndexed(created.id);
      }
    } catch (err) {
      $("list-status").textContent = String(err instanceof Error ? err.message : err);
    }
  });
  $("digest-btn").addEventListener("click", async () => {
    const pre = $("digest");
    pre.textContent = "\u52A0\u8F7D\u4E2D\u2026";
    try {
      const data = unwrap(await storefront.GET("/digest"));
      pre.textContent = JSON.stringify(data, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });
  $("counter-btn").addEventListener("click", async () => {
    const pre = $("hatchet");
    pre.textContent = "\u52A0\u8F7D\u4E2D\u2026";
    try {
      const data = unwrap(
        await catalog.POST("/counters/{id}", {
          params: { path: { id: "demo" } },
          body: { delta: 2 }
        })
      );
      pre.textContent = JSON.stringify({ actor: "demo", ...data }, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });
  $("heartbeat-btn").addEventListener("click", async () => {
    const pre = $("hatchet");
    pre.textContent = "\u52A0\u8F7D\u4E2D\u2026";
    try {
      const data = unwrap(await catalog.GET("/heartbeat"));
      pre.textContent = JSON.stringify(data, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });
  $("fanout-btn").addEventListener("click", async () => {
    const pre = $("hatchet");
    pre.textContent = "\u52A0\u8F7D\u4E2D\u2026";
    try {
      const data = unwrap(await catalog.GET("/article-fanout"));
      pre.textContent = JSON.stringify(data, null, 2);
    } catch (err) {
      pre.textContent = String(err instanceof Error ? err.message : err);
    }
  });
  $("probe-btn").addEventListener("click", async () => {
    const pre = $("probes");
    pre.textContent = "\u52A0\u8F7D\u4E2D\u2026";
    const out = {};
    try {
      const ping = unwrap(await catalog.GET("/public/ping"));
      out.publicPing = ping;
    } catch (err) {
      out.publicPing = String(err instanceof Error ? err.message : err);
    }
    try {
      out.me = unwrap(await catalog.GET("/me"));
    } catch (err) {
      out.me = String(err instanceof Error ? err.message : err);
    }
    try {
      const echo = await fetch(`${CATALOG}/chi/echo?msg=hi`);
      out.chiEcho = { status: echo.status, body: await echo.json(), chi: echo.headers.get("X-Fxkit-Chi") };
    } catch (err) {
      out.chiEcho = String(err instanceof Error ? err.message : err);
    }
    pre.textContent = JSON.stringify(out, null, 2);
  });
}
async function boot() {
  bindPageHandlers();
  await refreshList();
  const onCallback = window.location.pathname === "/callback" || window.location.pathname === "/callback/";
  if (onCallback) {
    try {
      await logto.handleSignInCallback(window.location.href);
    } catch (err) {
      $("auth-status").textContent = `\u767B\u5F55\u56DE\u8C03\u5931\u8D25\uFF1A${err instanceof Error ? err.message : err}`;
      $("sign-in").hidden = false;
      return;
    }
    history.replaceState({}, "", "/");
  }
  try {
    await refreshAccessToken();
  } catch (err) {
    setAccessToken("");
    $("sign-in").hidden = true;
    $("sign-out").hidden = false;
    $("bind-admin").hidden = false;
    $("auth-status").textContent = `\u5DF2\u767B\u5F55\u4F46\u62FF\u4E0D\u5230 access token\uFF08Logto \u63A7\u5236\u53F0\u8BF7\u7ED9\u5E94\u7528\u52FE\u9009 API Resource ${API_RESOURCE}\uFF09\uFF1A${err instanceof Error ? err.message : err}`;
    return;
  }
  await refreshAuthUi();
  if (accessToken) {
    await refreshList();
  }
}
boot().catch((err) => {
  const msg = String(err instanceof Error ? err.message : err);
  const certHint = /fetch|SSL|certificate|CERT|Failed to fetch|NetworkError/i.test(msg) ? `\u65E0\u6CD5\u8FDE\u63A5 Logto ${LOGTO_ENDPOINT}\u3002\u5148\u7528\u540C\u4E00\u6D4F\u89C8\u5668\u6253\u5F00\u8BE5\u5730\u5740\u5E76\u4FE1\u4EFB\u81EA\u7B7E\u8BC1\u4E66\uFF0C\u5E76\u5728\u63A7\u5236\u53F0\u914D\u7F6E CORS\u3002 ` : "";
  $("auth-status").textContent = certHint + msg;
  $("sign-in").hidden = false;
});
