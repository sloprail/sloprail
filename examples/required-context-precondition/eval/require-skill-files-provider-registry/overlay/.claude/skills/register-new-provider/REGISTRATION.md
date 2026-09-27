# Registering a provider

A new `<Name>Provider` class under `pydantic_ai_slim/pydantic_ai/providers/`
is inert until `infer_provider` in
`pydantic_ai_slim/pydantic_ai/providers/__init__.py` can resolve its string
name to the class. Add one `elif` branch, matching the existing ones:

```python
elif provider == 'deepseek':
    from .deepseek import DeepSeekProvider

    return DeepSeekProvider
```

Miss this and the module imports fine, its tests can even pass in isolation,
and `infer_model('<name>:...')` still raises `UserError` for every caller —
the provider is unreachable through the one path everyone actually uses it
through.
