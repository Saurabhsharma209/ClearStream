#ifndef RNNOISE_H
#define RNNOISE_H
#include <stddef.h>
#include <stdio.h>
typedef struct DenoiseState DenoiseState;
typedef struct RNNModel RNNModel;
int rnnoise_get_size(void);
int rnnoise_get_frame_size(void);
int rnnoise_init(DenoiseState *st, RNNModel *model);
RNNModel *rnnoise_model_from_filename(const char *filename);
RNNModel *rnnoise_model_from_file(FILE *f);
void rnnoise_model_free(RNNModel *model);
DenoiseState *rnnoise_create(RNNModel *model);
void rnnoise_destroy(DenoiseState *st);
float rnnoise_process_frame(DenoiseState *st, float *out, const float *in);
#endif
