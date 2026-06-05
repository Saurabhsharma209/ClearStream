"""
Export DeepFilterNet to ONNX for ClearStream's deepfilter backend.

Usage (run on Mac, NOT in sandbox — requires torch):
    pip install deepfilternet torch
    python scripts/export_deepfilter_onnx.py --out models/deepfilter.onnx

The exported model:
    Input  "input"  : [1, N] float32  (48kHz, normalised -1…1)
    Output "output" : [1, N] float32  (denoised)

ClearStream's deepfilter.go handles 16kHz↔48kHz resampling internally.
"""

import argparse
import os
import numpy as np

def export(out_path: str) -> None:
    import torch
    from df.enhance import init_df

    print("Loading DeepFilterNet...")
    model, df_state, _ = init_df()
    model.eval()
    sr = df_state.sr()
    hop = df_state.hop_size()
    print(f"  Sample rate: {sr} Hz | Hop size: {hop} samples")

    os.makedirs(os.path.dirname(out_path) or '.', exist_ok=True)

    # DeepFilterNet expects full-length audio, not single frames.
    # Export with dynamic sequence length.
    dummy = torch.zeros(1, sr)  # 1 second dummy

    torch.onnx.export(
        model,
        dummy,
        out_path,
        input_names=["input"],
        output_names=["output"],
        dynamic_axes={"input": {0: "batch", 1: "samples"}, "output": {0: "batch", 1: "samples"}},
        opset_version=14,
        do_constant_folding=True,
    )
    print(f"Exported DeepFilterNet ONNX → {out_path}")


def verify(path: str) -> None:
    import onnxruntime as ort
    sess = ort.InferenceSession(path)
    inp = np.random.randn(1, 48000).astype(np.float32) * 0.1
    out = sess.run(["output"], {"input": inp})[0]
    print(f"Verified: input {inp.shape} → output {out.shape}")
    print(f"Output range: [{out.min():.4f}, {out.max():.4f}]")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", default="models/deepfilter.onnx")
    parser.add_argument("--verify", action="store_true")
    args = parser.parse_args()
    export(args.out)
    if args.verify:
        verify(args.out)
