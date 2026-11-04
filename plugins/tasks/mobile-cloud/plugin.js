export const meta = {
  apiVersion: 1,
  key: "mobile-cloud",
  name: "Mobile Cloud",
  icon: "text:MC",
  description: { en: "Mobile Cloud Seedance video generation", zh: "移动云 Seedance 视频生成" },
  version: "1.0.2",
  author: { name: "Qiyuan" },
  models: ["seedance-2.0", "seedance-2.5"],
  fetchMode: "per_task",
  routes: [
    { method: "POST", path: "/mobile-cloud/v1/contents/generations/tasks", type: "submit", decode: "createTask", render: "taskCreated" },
    { method: "GET", path: "/mobile-cloud/v1/contents/generations/tasks/:task_id", type: "query", render: "taskStatus" },
  ],
  protocols: ["openai_video"],
};

function validateImageSource(value) {
  if ((typeof value === "string" && value.startsWith("request_file:")) || (value && typeof value === "object" && value.__fileRef !== undefined))
    throw new Error("Uploaded files are not supported; use a public HTTPS image URL or an asset:// reference");
  // The host enforces public-network access and ownership of existing assets.
  if (typeof value !== "string" || (!/^https:\/\/[^/?#\s\\@]+(?:[/?#][^\s\\]*)?$/i.test(value) && !/^asset:\/\/[^/\\?#%\s]+$/.test(value)))
    throw new Error("image URL must be a public HTTPS URL or an asset:// reference");
}

function videoBody(req, model) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("JSON object required");
  if (req.metadata !== undefined && (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata))) throw new Error("metadata must be an object");
  if (req.prompt !== undefined && typeof req.prompt !== "string") throw new Error("prompt must be a string");
  if (req.images !== undefined && !Array.isArray(req.images)) throw new Error("images must be an array");
  const images = [];
  if (req.images !== undefined) images.push(...req.images);
  if (req.image !== undefined) images.push(req.image);
  if (req.input_reference !== undefined) images.push(req.input_reference);
  for (const image of images) validateImageSource(image);
  const body = Object.assign({}, req.metadata || {});
  const fields = ["content", "resolution", "ratio", "duration", "generate_audio", "watermark", "seed", "camera_fixed"];
  for (const field of fields) {
    if (req[field] !== undefined) body[field] = req[field];
  }
  if (req.seconds !== undefined) {
    if (typeof req.seconds !== "number" && (typeof req.seconds !== "string" || !req.seconds.trim())) throw new Error("duration must be an integer between 1 and 3600");
    body.duration = Number(req.seconds);
  }
  if (body.duration !== undefined && (!Number.isInteger(body.duration) || body.duration < 1 || body.duration > 3600))
    throw new Error("duration must be an integer between 1 and 3600");
  if (body.content === undefined) {
    body.content = [];
    for (const url of images) body.content.push({ type: "image_url", image_url: { url: url } });
    if (typeof req.prompt === "string" && req.prompt.trim()) body.content.push({ type: "text", text: req.prompt });
  }
  if (!Array.isArray(body.content) || !body.content.length) throw new Error("content is required");
  for (const item of body.content) {
    if (!item || typeof item !== "object" || Array.isArray(item)) throw new Error("invalid video content item");
    if (item.type === "text" && typeof item.text === "string" && item.text.trim()) continue;
    if (item.type === "image_url" && item.image_url) {
      validateImageSource(item.image_url.url);
      continue;
    }
    throw new Error("content must contain text or image_url items");
  }
  body.model = model;
  return body;
}

function submitIntent(ctx, model) {
  if (!ctx.body) throw new Error("JSON object or form body required");
  let req;
  if (ctx.body.kind === "json") req = ctx.body.value;
  else if (ctx.body.kind === "multipart" || ctx.body.kind === "form") {
    if (ctx.body.files && ctx.body.files.length) throw new Error("Uploaded files are not supported; use a public HTTPS image URL or an asset:// reference");
    req = {};
    const fields = ctx.body.fields || {};
    for (const field of ["model", "prompt", "seconds", "image", "input_reference", "metadata"]) {
      if (fields[field] === undefined) continue;
      if (fields[field].length !== 1) throw new Error(field + " must have exactly one value");
      req[field] = fields[field][0];
    }
    if (fields.images !== undefined) req.images = Array.from(fields.images);
    if (req.metadata !== undefined) {
      try {
        req.metadata = JSON.parse(req.metadata);
      } catch (e) {
        throw new Error("metadata must be a JSON object");
      }
    }
  } else throw new Error("JSON object or form body required");
  if (typeof model !== "string" || !model.trim()) throw new Error("model is required");
  const body = videoBody(req, model);
  return { kind: "submit", model: model, action: body.content.some((item) => item.type === "image_url") ? "image_to_video" : "text_to_video", requestBody: body };
}

function taskView(task) {
  const output = Object.assign({}, task.data || {}, { id: task.task_id });
  const statuses = { NOT_START: "queued", QUEUED: "queued", IN_PROGRESS: "running", SUCCESS: "succeeded", FAILURE: "failed" };
  if (statuses[task.status]) output.status = statuses[task.status];
  if (task.fail_reason) output.error = Object.assign({}, output.error || {}, { message: task.fail_reason });
  delete output.model;
  return output;
}

export const native = {
  createTask: (ctx) => submitIntent(ctx, ctx.body && ctx.body.value && ctx.body.value.model),
  taskCreated: (ctx, task) => taskView(task),
  taskStatus: (ctx, task) => taskView(task),
};

function apiRoot(ctx) {
  return ctx.baseUrl.replace(/\/+$/, "").replace(/\/v1$/, "");
}

export function buildSubmitRequest(ctx) {
  const body = videoBody(ctx.requestBody, ctx.upstreamModel || ctx.model);
  return {
    url: apiRoot(ctx) + "/v1/contents/generations/tasks",
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: body,
    action: body.content.some((item) => item.type === "image_url") ? "image_to_video" : "text_to_video",
  };
}

export function parseSubmitResponse(ctx, response) {
  if (!response.body || typeof response.body.id !== "string" || !response.body.id.trim()) throw new Error("video task id is missing");
  return { taskId: response.body.id, taskData: response.body };
}

export function buildQueryRequest(ctx) {
  return { url: apiRoot(ctx) + "/v1/contents/generations/tasks/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Accept: "application/json", Authorization: "Bearer " + ctx.apiKey } };
}

export function parseTaskResult(ctx, body) {
  if (body.status === "queued") return { status: "QUEUED", progress: "0%" };
  if (body.status === "running") return { status: "IN_PROGRESS", progress: "50%" };
  if (body.status === "succeeded") return { status: "SUCCESS", progress: "100%", url: body.content && body.content.video_url || "" };
  if (body.status === "failed") return { status: "FAILURE", progress: "100%", reason: body.error && body.error.message || "Video generation failed" };
  return { status: "UNKNOWN" };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && task.data && task.data.content && task.data.content.video_url ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  return { url: ctx.data && ctx.data.content && ctx.data.content.video_url || "", method: ctx.clientRequest.method, credentialless: true };
}

export const protocols = {
  openai_video: {
    decodeRequest: (ctx) => submitIntent(ctx, ctx.model),
    render: (ctx, task) => task.data || {},
  },
};
