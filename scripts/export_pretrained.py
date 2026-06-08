#!/usr/bin/env python3
"""
export_pretrained.py — Export the pretrained DeepFilterNet model to ONNX.

This is the FASTEST way to get a real ML model into ClearStream — no training
needed. The pretrained model already handles HVAC, babble, keyboard noise.
Fine-tuning on Exotel data comes later to improve Indian English telephony.

Usage:
  python3 scripts/export_pretrained.py

Output:
  ~/ClearStream/models/deepfilter_pretrained.onnx  (~30MB)

Copy to: ~/ClearStream/scripts/export_pretrained.py
"""

import sys
from pathlib import Path

MODELS_DIR  = Path.home() / "ClearStream" / "models"
SAMPLE_RATE = 16000

def main():
    try:
        from df.enhance import init_df
    except ImportError:
        print("ERROR: deepfilternet not installed.")
        print("Run: pip3 install deepfilternet")
        sys.exit(1)

    import torch
    import onnx
    import onnxruntime as ort
    import numpy as np

    MODELS_DIR.mkdir(parents=True, exist_ok=True)
    onnx_path = MODELS_DIR / "deepfilter_pretrained.onnx"

    # Step 1: Load pretrained model
    print("Loading pretrained DeepFilterNet (downloads ~30MB on first run)…")
    model, df_state, _ = init_df()
    model.eval()
    print(f"Model loaded. Sample rate: {df_state.sr()} Hz")
    print(f"Frame size: {df_state.frame_size} samples ({df_state.frame_size/df_state.sr()*1000:.0f}ms)")

    # Step 2: Inspect what the model actually expects
    print("\nModel architecture:")
    total_params = sum(p.numel() for p in model.parameters())
    print(f"  Total parameters: {total_params/1e6:.1f}M")

    # Step 3: Try the df package's built-in ONNX export
    print(f"\nExporting to {onnx_path} …")

    # DeepFilterNet uses a streaming frame-based API internally.
    # We wrap it to accept raw PCM [1, N] and return enhanced PCM [1, N].
    class DeepFilterWrapper(torch.nn.Module):
        """
        Wraps DeepFilterNet to take raw 16kHz PCM and return enhanced PCM.
        This matches the interface expected by pkg/model/deepfilter.go.
        Input:  float32 [1, N] PCM in range [-1, 1]
        Output: float32 [1, N] enhanced PCM in range [-1, 1]
        """
        def __init__(self, model, df_state):
            super().__init__()
            self.model    = model
            self.df_state = df_state

        def forward(self, pcm: torch.Tensor) -> torch.Tensor:
            from df.enhance import enhance
            return enhance(self.model, self.df_state, pcm)

    wrapper = DeepFilterWrapper(model, df_state)
    wrapper.eval()

    # Test forward pass first
    print("Testing forward pass…")
    dummy_pcm = torch.zeros(1, SAMPLE_RATE)  # 1 second of silence
    with torch.no_grad():
        try:
            out = wrapper(dummy_pcm)
            print(f"  Forward pass OK: input {dummy_pcm.shape} → output {out.shape}")
        except Exception as e:
            print(f"  Forward pass failed: {e}")
            print("  Trying direct model export instead…")
            export_direct(model, df_state, onnx_path)
            return

    # Export to ONNX
    try:
        torch.onnx.export(
            wrapper,
            (dummy_pcm,),
            str(onnx_path),
            input_names=["input"],
            output_names=["output"],
            dynamic_axes={
                "input":  {0: "batch", 1: "samples"},
                "output": {0: "batch", 1: "samples"},
            },
            opset_version=14,
            do_constant_folding=True,
        )
        print(f"ONNX export OK → {onnx_path} ({onnx_path.stat().st_size/1e6:.1f} MB)")
    except Exception as e:
        print(f"ONNX wrapper export failed: {e}")
        print("Trying direct export…")
        export_direct(model, df_state, onnx_path)
        return

    # Step 4: Verify with onnxruntime
    print("\nVerifying with ONNX Runtime…")
    try:
        sess = ort.InferenceSession(str(onnx_path))
        inp  = sess.get_inputs()[0]
        out  = sess.get_outputs()[0]
        print(f"  Input:  {inp.name}  shape={inp.shape}  type={inp.type}")
        print(f"  Output: {out.name}  shape={out.shape}  type={out.type}")

        test_audio = np.random.randn(1, SAMPLE_RATE).astype(np.float32)
        result = sess.run([out.name], {inp.name: test_audio})[0]
        print(f"  Inference OK: {test_audio.shape} → {result.shape}")
        print(f"  Output range: [{result.min():.3f}, {result.max():.3f}]")
    except Exception as e:
        print(f"  ONNX Runtime verification failed: {e}")

    # Step 5: Print ClearStream integration instructions
    print(f"""
╔══════════════════════════════════════════════════════════════════╗
║  Model exported successfully!                                    ║
╠══════════════════════════════════════════════════════════════════╣
║  File: {str(onnx_path):<55} ║
╠══════════════════════════════════════════════════════════════════╣
║  To use in ClearStream, update your Config:                      ║
║                                                                  ║
║    cs, _ := clearstream.New(clearstream.Config{{                  ║
║      Suppressor: model.SuppressorConfig{{                         ║
║        Backend:   "deepfilter",                                  ║
║        ModelPath: "{str(onnx_path):<42}",║
║      }},                                                         ║
║    }})                                                           ║
║                                                                  ║
║  Build with: go build -tags onnx ./...                          ║
╚══════════════════════════════════════════════════════════════════╝

Next: Run A/B test with:
  go test -tags onnx -run TestABDeepFilter ./pkg/model/...
""")


def export_direct(model, df_state, onnx_path: Path):
    """
    Fallback: export the raw DeepFilterNet internals.
    The Go side will need to handle STFT pre/post-processing.
    """
    import torch

    print("Inspecting model inputs for direct export…")
    for name, module in model.named_modules():
        if hasattr(module, 'forward'):
            print(f"  {name}: {type(module).__name__}")

    # Save as PyTorch format for manual inspection
    pt_path = onnx_path.with_suffix(".pt")
    torch.save({
        "model_state": model.state_dict(),
        "df_config": {
            "sr": df_state.sr(),
            "fft_size": df_state.fft_size(),
            "hop_size": df_state.hop_size(),
            "nb_erb": df_state.nb_erb(),
            "nb_df": df_state.nb_df(),
        }
    }, pt_path)
    print(f"Saved PyTorch checkpoint: {pt_path}")
    print("Use this to inspect the model architecture and write a custom ONNX wrapper.")

    # Print df_state config for Go implementation
    print("\nDeepFilterNet config (needed for pkg/model/deepfilter.go):")
    print(f"  Sample rate:    {df_state.sr()} Hz")
    print(f"  FFT size:       {df_state.fft_size()}")
    print(f"  Hop size:       {df_state.hop_size()}")
    print(f"  ERB bands:      {df_state.nb_erb()}")
    print(f"  DF bands:       {df_state.nb_df()}")


if __name__ == "__main__":
    main()
