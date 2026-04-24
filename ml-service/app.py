from fastapi import FastAPI
from pydantic import BaseModel
from typing import Dict, List, Any
import numpy as np
import os
import json
import joblib
from sklearn.ensemble import IsolationForest
from sklearn.svm import OneClassSVM
from sklearn.preprocessing import StandardScaler
from scipy.stats import zscore
import pandas as pd

CONFIG_PATH = os.getenv("FEATURE_CONFIG", "/app/config/features.json")
MODEL_DIR = os.getenv("MODEL_DIR", "/app/model")

os.makedirs(MODEL_DIR, exist_ok=True)
app = FastAPI(title="Full ML service")


class TrainRequest(BaseModel):
    feature_order: List[str]
    samples: List[Dict[str, float]]


class PredictRequest(BaseModel):
    metrics: Dict[str, float]


with open(CONFIG_PATH, "r", encoding="utf-8") as f:
    FEATURE_CONFIG = json.load(f)

FEATURE_ORDER = [x["name"] for x in FEATURE_CONFIG["features"]]
FEATURE_WEIGHTS = {x["name"]: x["weight"] for x in FEATURE_CONFIG["features"]}
FEATURE_MODELS = {x["name"]: x["models"] for x in FEATURE_CONFIG["features"]}

state: Dict[str, Any] = {
    "feature_order": FEATURE_ORDER,
    "scaler": None,
    "iforest": None,
    "ocsvm": None,
    "baseline_mean": {},
    "baseline_std": {},
    "ewma": {},
    "hw_seasonal": {},
}

state["loaded_from_disk"] = False
state["trained_in_session"] = False

JOBLIB_KEYS = ["scaler", "iforest", "ocsvm"]
META_KEYS = [
    "feature_order",
    "baseline_mean",
    "baseline_std",
    "ewma",
    "hw_seasonal",
]


def save_all():
    os.makedirs(MODEL_DIR, exist_ok=True)
    for k in JOBLIB_KEYS:
        if state.get(k) is not None:
            joblib.dump(state[k], f"{MODEL_DIR}/{k}.joblib")
    meta = {k: state.get(k) for k in META_KEYS}
    with open(f"{MODEL_DIR}/meta.json", "w", encoding="utf-8") as f:
        json.dump(meta, f, ensure_ascii=False, indent=2)


def load_all():
    try:
        loaded_any = False
        for k in JOBLIB_KEYS:
            p = f"{MODEL_DIR}/{k}.joblib"
            if os.path.exists(p):
                state[k] = joblib.load(p)
                loaded_any = True
                state[k] = joblib.load(p)
        mp = f"{MODEL_DIR}/meta.json"
        if os.path.exists(mp):
            loaded_any = True
            with open(mp, "r", encoding="utf-8") as f:
                meta = json.load(f)
            for k in META_KEYS:
                if k in meta:
                    state[k] = meta[k]
        state["loaded_from_disk"] = loaded_any
    except Exception:
        pass


@app.on_event("startup")
def startup():
    load_all()


@app.get("/health")
def health():
    models = {
        "scaler": state["scaler"] is not None,
        "iforest": state["iforest"] is not None,
        "ocsvm": state["ocsvm"] is not None,
        "feature_order": len(state["feature_order"]) > 0,
        "baseline_mean": bool(state["baseline_mean"]),
        "baseline_std": bool(state["baseline_std"]),
        "ewma": bool(state["ewma"]),
        "hw_seasonal": bool(state["hw_seasonal"]),
    }
    return {
        "status": "ok",
        "feature_order": state["feature_order"],
        "feature_models": FEATURE_MODELS,
        "models": models,
        "trained": all(models.values()),
        "loaded_from_disk": state["loaded_from_disk"],
        "trained_in_session": state["trained_in_session"],
    }


@app.post("/train")
def train(req: TrainRequest):
    rows = req.samples
    feature_order = req.feature_order or FEATURE_ORDER

    X = np.array([[float(row.get(f, 0.0)) for f in feature_order]
                 for row in rows], dtype=float)

    scaler = StandardScaler()
    Xs = scaler.fit_transform(X)

    iforest = IsolationForest(
        n_estimators=200,
        contamination=0.05,
        random_state=42
    )
    iforest.fit(Xs)

    ocsvm = OneClassSVM(kernel="rbf", gamma="scale", nu=0.05)
    ocsvm.fit(Xs)

    baseline_mean = {}
    baseline_std = {}
    ewma = {}
    hw_seasonal = {}

    for i, f in enumerate(feature_order):
        col = X[:, i]
        baseline_mean[f] = float(np.mean(col))
        baseline_std[f] = float(np.std(col) + 1e-6)
        ewma[f] = float(pd.Series(col).ewm(span=12).mean().iloc[-1])

        season = min(24, len(col))
        if season > 1:
            hw_seasonal[f] = [float(x) for x in pd.Series(col).rolling(
                season).mean().fillna(method="bfill").tail(season).tolist()]
        else:
            hw_seasonal[f] = [float(np.mean(col))]

    state["feature_order"] = feature_order
    state["scaler"] = scaler
    state["iforest"] = iforest
    state["ocsvm"] = ocsvm
    state["baseline_mean"] = baseline_mean
    state["baseline_std"] = baseline_std
    state["ewma"] = ewma
    state["hw_seasonal"] = hw_seasonal

    save_all()

    state["trained_in_session"] = True

    return {
        "status": "ok",
        "trained_features": feature_order,
        "samples": len(rows)
    }


@app.post("/predict")
def predict(req: PredictRequest):
    x = np.array([req.metrics.get(k, 0.0) for k in FEATURE_ORDER], dtype=float)
    x2 = x.reshape(1, -1)

    method_scores: Dict[str, Dict[str, float]] = {
        "zscore": {},
        "ewma": {},
        "holt_winters": {},
        "threshold": {},
        "iforest": {},
        "ocsvm": {},
    }

    # one-dimensional methods by third metrics
    for i, feature in enumerate(FEATURE_ORDER):
        value = float(x[i])

        mean = float(state["baseline_mean"].get(feature, value))
        std = float(state["baseline_std"].get(feature, 1.0)) or 1.0
        ew = float(state["ewma"].get(feature, mean))
        hw = state["hw_seasonal"].get(feature, mean)

        if isinstance(hw, list):
            hw_ref = float(hw[-1]) if hw else float(mean)
        else:
            hw_ref = float(hw)

        # z-score
        z = abs(value - mean) / max(std, 1e-6)
        method_scores["zscore"][feature] = min(1.0, z / 3.0)

        # EWMA
        ewma_dev = abs(value - ew) / max(abs(mean), std, 1e-6)
        method_scores["ewma"][feature] = min(1.0, ewma_dev)

        # Holt-Winters proxy
        hw_dev = abs(value - hw_ref) / max(abs(mean), std, 1e-6)
        method_scores["holt_winters"][feature] = min(1.0, hw_dev)

        # Threshold
        if feature in ("if_down_ratio", "reboot_indicator"):
            method_scores["threshold"][feature] = 1.0 if value > 0 else 0.0
        elif feature in ("if_error_rate", "if_discard_rate", "temp_celsius", "cpu_load", "mem_util"):
            method_scores["threshold"][feature] = 1.0 if value > mean + \
                3 * std else 0.0
        else:
            method_scores["threshold"][feature] = 0.0

    # multidimensional models - are calculated on the entire vector
    if state["scaler"] is not None:
        xs = state["scaler"].transform(x2)
    else:
        xs = x2

    if state["iforest"] is not None:
        raw_if = -float(state["iforest"].score_samples(xs)[0])
        if_score = 1.0 / (1.0 + np.exp(-raw_if))
    else:
        if_score = 0.0

    if state["ocsvm"] is not None:
        raw_sv = -float(state["ocsvm"].decision_function(xs)[0])
        svm_score = 1.0 / (1.0 + np.exp(-raw_sv))
    else:
        svm_score = 0.0

    for feature in FEATURE_ORDER:
        method_scores["iforest"][feature] = if_score
        method_scores["ocsvm"][feature] = svm_score

    # total anomaly by metric - only for allowed models
    per_metric: Dict[str, float] = {}
    for feature in FEATURE_ORDER:
        allowed = FEATURE_MODELS.get(feature, [])

        vals = []
        for method in allowed:
            if method in method_scores and feature in method_scores[method]:
                vals.append(float(method_scores[method][feature]))

        per_metric[feature] = float(sum(vals) / len(vals)) if vals else 0.0

    return {
        "methods": method_scores,
        "per_metric": per_metric,
        "feature_models": FEATURE_MODELS,
    }


@app.post("/predict")
def predict(req: PredictRequest):
    fo = state["feature_order"]
    if not fo or state["scaler"] is None:
        return {"error": "model_not_trained"}
    x = np.array([[float(req.metrics.get(f, 0.0)) for f in fo]], dtype=float)
    xs = state["scaler"].transform(x)
    z_scores = {}
    ewma_scores = {}
    hw_scores = {}
    for i, f in enumerate(fo):
        mu = state["baseline_mean"][f]
        sd = state["baseline_std"][f]
        z = abs((x[0, i] - mu)/sd)
        z_scores[f] = float(max(0.0, min(1.0, z/6.0)))
        ew = state["ewma"][f]
        ew_dev = abs(x[0, i]-ew)/(abs(mu)+sd+1e-6)
        ewma_scores[f] = float(max(0.0, min(1.0, ew_dev/2.5)))
        seasonals = state["hw_seasonal"][f]
        ref = seasonals[0] if seasonals else mu
        hw_dev = abs(x[0, i]-ref)/(abs(mu)+sd+1e-6)
        hw_scores[f] = float(max(0.0, min(1.0, hw_dev/2.5)))
    if_raw = -float(state["iforest"].score_samples(xs)[0])
    if_score = float(max(0.0, min(1.0, (if_raw-0.35)/0.35)))
    svm_raw = -float(state["ocsvm"].score_samples(xs)[0])
    svm_score = float(max(0.0, min(1.0, (svm_raw+5.0)/10.0)))
    hybrid_per_metric = {}
    for f in fo:
        hybrid_per_metric[f] = float(
            0.20*z_scores[f] + 0.20*ewma_scores[f] + 0.15*hw_scores[f] + 0.25*if_score + 0.20*svm_score)
    global_score = float(np.mean(list(hybrid_per_metric.values())))
    return {
        "anomaly_score": global_score,
        "methods": {
            "zscore": z_scores,
            "ewma": ewma_scores,
            "holt_winters_proxy": hw_scores,
            "isolation_forest": if_score,
            "one_class_svm": svm_score,
            "hybrid": hybrid_per_metric,
        },
        "per_metric": hybrid_per_metric,
    }


if __name__ == '__main__':
    import uvicorn
    uvicorn.run(app, host='0.0.0.0', port=8000)
