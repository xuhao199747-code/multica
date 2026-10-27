-- Standard project role mapping for squad members. Optional enum of
-- prd | developer | reviewer (mika is always project-local, never squad-sourced).
-- Free-text role is untouched and keeps serving squad-internal context.
ALTER TABLE squad_member ADD COLUMN standard_role TEXT;
