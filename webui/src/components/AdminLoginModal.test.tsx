import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { AdminLoginModal } from './AdminLoginModal';

describe('AdminLoginModal', () => {
  it('uses standard password-manager credential fields', () => {
    render(<AdminLoginModal open busy={false} onClose={() => {}} onLogin={vi.fn()} />);
    expect(screen.getByLabelText('Username')).toHaveAttribute('name', 'username');
    expect(screen.getByLabelText('Username')).toHaveAttribute('autocomplete', 'username');
    expect(screen.getByLabelText('Password')).toHaveAttribute('name', 'password');
    expect(screen.getByLabelText('Password')).toHaveAttribute('autocomplete', 'current-password');
    expect(screen.getByLabelText('Password')).toHaveAttribute('type', 'password');
  });

  it('submits credentials and closes after success', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<AdminLoginModal open busy={false} onClose={onClose} onLogin={onLogin} />);
    await user.type(screen.getByLabelText('Username'), 'admin');
    await user.type(screen.getByLabelText('Password'), 'secret');
    await user.click(screen.getByRole('button', { name: 'Log in' }));
    expect(onLogin).toHaveBeenCalledWith('admin', 'secret');
    expect(onClose).toHaveBeenCalled();
  });

  it('shows login failures without clearing the username', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn().mockRejectedValue(new Error('Invalid username or password'));
    render(<AdminLoginModal open busy={false} onClose={() => {}} onLogin={onLogin} />);
    await user.type(screen.getByLabelText('Username'), 'admin');
    await user.type(screen.getByLabelText('Password'), 'wrong');
    await user.click(screen.getByRole('button', { name: 'Log in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid username or password');
    expect(screen.getByLabelText('Username')).toHaveValue('admin');
  });
});
