import path from 'node:path'
import { fileURLToPath } from 'node:url'

import _import from 'eslint-plugin-import'
import unicorn from 'eslint-plugin-unicorn'
import jsdoc from 'eslint-plugin-jsdoc'
import preferArrow from 'eslint-plugin-prefer-arrow'
import react from 'eslint-plugin-react'
import typescriptEslint from '@typescript-eslint/eslint-plugin'
import { fixupPluginRules } from '@eslint/compat'
import globals from 'globals'
import babelParser from '@babel/eslint-parser'
import tsParser from '@typescript-eslint/parser'
import js from '@eslint/js'
import { FlatCompat } from '@eslint/eslintrc'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)
const compat = new FlatCompat({
  baseDirectory: __dirname,
  recommendedConfig: js.configs.recommended,
  allConfig: js.configs.all,
})

export default [
  {
    ignores: ['**/dist', '**/coverage', 'packages/contracts/hardhat'],
  },
  ...compat.extends('plugin:prettier/recommended'),
  {
    plugins: {
      import: fixupPluginRules(_import),
      unicorn,
      jsdoc,
      'prefer-arrow': preferArrow,
      react,
      '@typescript-eslint': typescriptEslint,
    },

    languageOptions: {
      globals: {
        ...globals.browser,
      },

      parser: babelParser,
      ecmaVersion: 6,
      sourceType: 'module',

      parserOptions: {
        es6: true,
        requireConfigFile: false,
      },
    },

    rules: {
      'prettier/prettier': 'warn',
      'arrow-parens': ['off', 'always'],
      'brace-style': ['off', 'off'],
      'comma-dangle': 'off',
      complexity: 'off',
      'constructor-super': 'error',
      curly: 'error',
      'dot-notation': 'off',
      'eol-last': 'off',
      eqeqeq: ['error', 'smart'],
      'guard-for-in': 'error',
      'id-blacklist': 'off',
      'id-match': 'off',
      'import/no-extraneous-dependencies': ['error'],
      'import/no-internal-modules': 'off',

      'import/order': [
        'error',
        {
          groups: ['builtin', 'external', 'internal'],
          'newlines-between': 'always',
        },
      ],

      indent: 'off',
      'jsdoc/check-alignment': 'error',
      'jsdoc/check-indentation': 'error',
      'jsdoc/tag-lines': 'error',
      'linebreak-style': 'off',
      'max-classes-per-file': 'off',
      'max-len': 'off',
      'new-parens': 'off',
      'newline-per-chained-call': 'off',
      'no-bitwise': 'off',
      'no-caller': 'error',
      'no-cond-assign': 'error',
      'no-console': 'off',
      'no-debugger': 'error',
      'no-duplicate-case': 'error',
      'no-duplicate-imports': 'error',
      'no-empty': 'error',
      'no-eval': 'error',
      'no-extra-bind': 'error',
      'no-extra-semi': 'off',
      'no-fallthrough': 'off',
      'no-invalid-this': 'off',
      'no-irregular-whitespace': 'off',
      'no-multiple-empty-lines': 'off',
      'no-new-func': 'error',
      'no-new-wrappers': 'error',
      'no-redeclare': 'error',
      'no-return-await': 'error',
      'no-sequences': 'error',
      'no-sparse-arrays': 'error',
      'no-template-curly-in-string': 'error',
      'no-throw-literal': 'error',
      'no-trailing-spaces': 'off',
      'no-undef-init': 'error',
      'no-underscore-dangle': 'off',
      'no-unsafe-finally': 'error',
      'no-unused-expressions': 'off',
      'no-unused-labels': 'error',
      'no-use-before-define': 'off',
      'no-var': 'error',
      'object-shorthand': 'error',
      'one-var': ['error', 'never'],

      'padded-blocks': [
        'off',
        {
          blocks: 'never',
        },
        {
          allowSingleLineBlocks: true,
        },
      ],

      'prefer-arrow/prefer-arrow-functions': 'error',
      'prefer-const': 'error',
      'prefer-object-spread': 'error',
      'quote-props': 'off',
      quotes: 'off',
      radix: 'error',
      'react/jsx-curly-spacing': 'off',
      'react/jsx-equals-spacing': 'off',

      'react/jsx-tag-spacing': [
        'off',
        {
          afterOpening: 'allow',
          closingSlash: 'allow',
        },
      ],

      'react/jsx-wrap-multilines': 'off',
      semi: 'off',
      'space-before-blocks': 'error',
      'space-before-function-paren': 'off',
      'space-in-parens': ['off', 'never'],
      'unicorn/prefer-ternary': 'off',
      'use-isnan': 'error',
      'valid-typeof': 'off',
    },
  },
  {
    files: ['**/*.ts'],

    languageOptions: {
      parser: tsParser,
      ecmaVersion: 5,
      sourceType: 'module',

      parserOptions: {
        project: './packages/**/tsconfig.json',
        allowAutomaticSingleRunInference: true,
      },
    },

    rules: {
      '@typescript-eslint/adjacent-overload-signatures': 'error',
      '@typescript-eslint/array-type': 'off',
      '@typescript-eslint/ban-types': 'off',
      '@typescript-eslint/consistent-type-assertions': 'error',
      '@typescript-eslint/dot-notation': 'off',
      '@typescript-eslint/indent': 'off',

      '@typescript-eslint/member-delimiter-style': [
        'off',
        {
          multiline: {
            delimiter: 'none',
            requireLast: true,
          },

          singleline: {
            delimiter: 'semi',
            requireLast: false,
          },
        },
      ],

      '@typescript-eslint/member-ordering': 'off',
      '@typescript-eslint/naming-convention': 'off',
      '@typescript-eslint/no-empty-function': 'error',
      '@typescript-eslint/no-empty-interface': 'off',
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-misused-new': 'error',
      '@typescript-eslint/no-namespace': 'error',
      '@typescript-eslint/no-parameter-properties': 'off',

      '@typescript-eslint/no-shadow': [
        'error',
        {
          hoist: 'all',
        },
      ],

      '@typescript-eslint/no-this-alias': 'error',
      '@typescript-eslint/no-unused-expressions': 'off',
      '@typescript-eslint/no-use-before-define': 'off',
      '@typescript-eslint/no-var-requires': 'error',
      '@typescript-eslint/prefer-for-of': 'error',
      '@typescript-eslint/prefer-function-type': 'error',
      '@typescript-eslint/prefer-namespace-keyword': 'error',
      '@typescript-eslint/quotes': 'off',
      '@typescript-eslint/semi': ['off', null],

      '@typescript-eslint/triple-slash-reference': [
        'error',
        {
          path: 'always',
          types: 'prefer-import',
          lib: 'always',
        },
      ],

      '@typescript-eslint/type-annotation-spacing': 'off',
      '@typescript-eslint/unified-signatures': 'error',
      '@typescript-eslint/no-unused-vars': 'error',
    },
  },
]
